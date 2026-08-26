package meta

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Governor slows outgoing Graph requests as Meta's rate-limit headers
// approach their ceiling.
//
// Meta reports usage on every response, so pressure is knowable before a
// block happens. Reacting only to 4/17/32/613/80000-series errors - which is
// what Meta_Tracking does - means discovering the limit by hitting it, and
// the penalty is measured in minutes of total lockout.
type Governor interface {
	// Observe records a reading. Never blocks.
	Observe(key string, response ResponseMeta, now time.Time)
	// Wait sleeps for as long as current pressure warrants, or until ctx ends.
	Wait(ctx context.Context, key string, now time.Time) error
	// BlockedUntil reports an active hard block, so a scheduler can skip the
	// account instead of queuing work that will fail.
	BlockedUntil(key string) (time.Time, bool)
}

// ThrottlePolicy defines when to slow down and by how much.
type ThrottlePolicy struct {
	// SoftThreshold is the pressure above which delay is applied at all.
	SoftThreshold float64
	// HardThreshold is where the maximum delay applies.
	HardThreshold float64
	BaseDelay     time.Duration
	MaxDelay      time.Duration
}

// DefaultThrottlePolicy leaves the first three quarters of the budget
// unthrottled - typical traffic should pay nothing - then ramps steeply, so
// the last quarter is spent slowly enough to survive the window.
func DefaultThrottlePolicy() ThrottlePolicy {
	return ThrottlePolicy{
		SoftThreshold: 0.75,
		HardThreshold: 0.90,
		BaseDelay:     2 * time.Second,
		MaxDelay:      30 * time.Second,
	}
}

// Delay returns how long a caller should pause at the given pressure.
func (p ThrottlePolicy) Delay(pressure float64) time.Duration {
	if pressure < p.SoftThreshold {
		return 0
	}
	if pressure >= p.HardThreshold {
		return p.MaxDelay
	}
	span := p.HardThreshold - p.SoftThreshold
	if span <= 0 {
		return p.MaxDelay
	}
	ratio := (pressure - p.SoftThreshold) / span
	return time.Duration(float64(p.BaseDelay) * ratio)
}

type governorState struct {
	pressure     float64
	blockedUntil time.Time
	observedAt   time.Time
}

// MemoryGovernor keeps per-key state in memory.
//
// In-memory is sufficient because the readings are per-token and refresh on
// every response; a restarted worker relearns pressure on its first request.
// A hard block is additionally persisted to ad_account_sync_state by the
// caller, since that one is expensive to rediscover.
type MemoryGovernor struct {
	policy ThrottlePolicy
	sleep  func(context.Context, time.Duration) error

	mutex sync.Mutex
	state map[string]*governorState
}

func NewMemoryGovernor(policy ThrottlePolicy) *MemoryGovernor {
	if policy.MaxDelay <= 0 {
		policy = DefaultThrottlePolicy()
	}
	return &MemoryGovernor{
		policy: policy,
		sleep:  sleepContext,
		state:  make(map[string]*governorState),
	}
}

func (g *MemoryGovernor) Observe(key string, response ResponseMeta, now time.Time) {
	usage, ok := WorstUsage(response)
	if !ok {
		return
	}
	g.mutex.Lock()
	defer g.mutex.Unlock()

	entry, exists := g.state[key]
	if !exists {
		entry = &governorState{}
		g.state[key] = entry
	}
	entry.pressure = usage.Pressure()
	entry.observedAt = now
	if until, blocked := usage.BlockedUntil(now); blocked {
		entry.blockedUntil = until
	}
}

func (g *MemoryGovernor) Wait(ctx context.Context, key string, now time.Time) error {
	g.mutex.Lock()
	entry, ok := g.state[key]
	var pressure float64
	var blockedUntil time.Time
	if ok {
		pressure = entry.pressure
		blockedUntil = entry.blockedUntil
	}
	g.mutex.Unlock()

	if !ok {
		return nil
	}
	if blockedUntil.After(now) {
		// Cap the in-request wait: a multi-minute block is the scheduler's
		// problem to route around, not something to hold a worker slot for.
		remaining := blockedUntil.Sub(now)
		if remaining > g.policy.MaxDelay {
			remaining = g.policy.MaxDelay
		}
		return g.sleep(ctx, remaining)
	}
	delay := g.policy.Delay(pressure)
	if delay <= 0 {
		return nil
	}
	return g.sleep(ctx, delay)
}

func (g *MemoryGovernor) BlockedUntil(key string) (time.Time, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	entry, ok := g.state[key]
	if !ok || entry.blockedUntil.IsZero() {
		return time.Time{}, false
	}
	return entry.blockedUntil, true
}

// Pressure exposes the last reading for a key, for diagnostics.
func (g *MemoryGovernor) Pressure(key string) (float64, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	entry, ok := g.state[key]
	if !ok {
		return 0, false
	}
	return entry.pressure, true
}

// GovernorKey buckets requests by the quota they consume. Ad-account calls
// are isolated per account so one throttled account does not slow every other
// account sharing the app budget.
func GovernorKey(graphPath string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(graphPath), "/")
	if index := strings.Index(trimmed, "?"); index >= 0 {
		trimmed = trimmed[:index]
	}
	// A paging URL arrives absolute; keep only the path.
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		if index := strings.Index(trimmed, "/act_"); index >= 0 {
			trimmed = trimmed[index+1:]
		}
	}
	for _, segment := range strings.Split(trimmed, "/") {
		if strings.HasPrefix(segment, "act_") {
			return "account:" + segment
		}
	}
	return "app"
}
