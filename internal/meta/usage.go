package meta

import (
	"encoding/json"
	"strconv"
	"time"
)

// Usage is a normalized reading of Meta's rate-limit headers.
//
// The three headers carry three different shapes, which is why ResponseMeta
// keeps them as raw JSON and the parsing lives here:
//
//	X-App-Usage               {"call_count":25,"total_cputime":12,"total_time":8}
//	X-Ad-Account-Usage        {"acc_id_util_pct":9.67,"reset_time_duration":100,
//	                           "ads_api_access_tier":"standard_access"}
//	X-Business-Use-Case-Usage {"<business_id>":[{"type":"ads_insights",
//	                           "call_count":10,"estimated_time_to_regain_access":0}]}
type Usage struct {
	CallCount    float64
	TotalCPUTime float64
	TotalTime    float64
	// EstimatedTimeToRegainAccess is in minutes and non-zero only when Meta
	// has already blocked the caller.
	EstimatedTimeToRegainAccess int
	// ResetTimeDuration is in seconds, ad-account header only.
	ResetTimeDuration int
	Tier              string
	Type              string
}

// Pressure is the highest of the percentage counters, expressed as 0..1.
// Meta blocks at 100 on any one of them, so the maximum is the number that
// matters, not the average.
func (u Usage) Pressure() float64 {
	highest := u.CallCount
	if u.TotalCPUTime > highest {
		highest = u.TotalCPUTime
	}
	if u.TotalTime > highest {
		highest = u.TotalTime
	}
	if highest < 0 {
		return 0
	}
	return highest / 100
}

// Blocked reports whether Meta has already cut the caller off.
func (u Usage) Blocked() bool { return u.EstimatedTimeToRegainAccess > 0 }

// BlockedUntil converts the estimate into a wall-clock instant.
func (u Usage) BlockedUntil(now time.Time) (time.Time, bool) {
	if !u.Blocked() {
		return time.Time{}, false
	}
	return now.Add(time.Duration(u.EstimatedTimeToRegainAccess) * time.Minute), true
}

// ParseAppUsage reads X-App-Usage, the app-wide budget.
func ParseAppUsage(response ResponseMeta) (Usage, bool) {
	if len(response.AppUsage) == 0 {
		return Usage{}, false
	}
	return usageFromFields(response.AppUsage), true
}

// ParseAdAccountUsage reads X-Ad-Account-Usage. It reports a single
// utilisation percentage rather than the three counters of the app header.
func ParseAdAccountUsage(response ResponseMeta) (Usage, bool) {
	if len(response.AdAccountUsage) == 0 {
		return Usage{}, false
	}
	usage := Usage{
		CallCount:         jsonNumber(response.AdAccountUsage, "acc_id_util_pct"),
		ResetTimeDuration: int(jsonNumber(response.AdAccountUsage, "reset_time_duration")),
		Tier:              jsonString(response.AdAccountUsage, "ads_api_access_tier"),
	}
	return usage, true
}

// ParseBusinessUsage reads X-Business-Use-Case-Usage, which maps a business
// ID to an array of per-use-case readings. The worst reading across all
// businesses and use cases is returned, since any one of them can block.
func ParseBusinessUsage(response ResponseMeta) (Usage, bool) {
	if len(response.BusinessUsage) == 0 {
		return Usage{}, false
	}
	var worst Usage
	found := false
	for _, encoded := range response.BusinessUsage {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &entries); err != nil {
			continue
		}
		for _, entry := range entries {
			usage := usageFromFields(entry)
			usage.Type = jsonString(entry, "type")
			found = true
			if !worst.Blocked() && usage.Blocked() {
				worst = usage
				continue
			}
			if usage.Pressure() > worst.Pressure() {
				blocked := worst.EstimatedTimeToRegainAccess
				worst = usage
				if blocked > worst.EstimatedTimeToRegainAccess {
					worst.EstimatedTimeToRegainAccess = blocked
				}
			}
		}
	}
	return worst, found
}

// WorstUsage combines all three headers into the single most pessimistic
// reading, which is what a throttling decision should act on.
func WorstUsage(response ResponseMeta) (Usage, bool) {
	var worst Usage
	found := false
	for _, parse := range []func(ResponseMeta) (Usage, bool){
		ParseAppUsage, ParseAdAccountUsage, ParseBusinessUsage,
	} {
		usage, ok := parse(response)
		if !ok {
			continue
		}
		found = true
		if usage.Pressure() > worst.Pressure() {
			blocked := worst.EstimatedTimeToRegainAccess
			worst = usage
			if blocked > worst.EstimatedTimeToRegainAccess {
				worst.EstimatedTimeToRegainAccess = blocked
			}
			continue
		}
		if usage.EstimatedTimeToRegainAccess > worst.EstimatedTimeToRegainAccess {
			worst.EstimatedTimeToRegainAccess = usage.EstimatedTimeToRegainAccess
		}
	}
	return worst, found
}

func usageFromFields(fields map[string]json.RawMessage) Usage {
	return Usage{
		CallCount:                   jsonNumber(fields, "call_count"),
		TotalCPUTime:                jsonNumber(fields, "total_cputime"),
		TotalTime:                   jsonNumber(fields, "total_time"),
		EstimatedTimeToRegainAccess: int(jsonNumber(fields, "estimated_time_to_regain_access")),
	}
}

// jsonNumber accepts a JSON number or a numeric string, because Meta is not
// consistent about which it sends.
func jsonNumber(fields map[string]json.RawMessage, key string) float64 {
	encoded, ok := fields[key]
	if !ok {
		return 0
	}
	var number float64
	if err := json.Unmarshal(encoded, &number); err == nil {
		return number
	}
	var text string
	if err := json.Unmarshal(encoded, &text); err == nil {
		parsed, parseErr := strconv.ParseFloat(text, 64)
		if parseErr == nil {
			return parsed
		}
	}
	return 0
}

func jsonString(fields map[string]json.RawMessage, key string) string {
	encoded, ok := fields[key]
	if !ok {
		return ""
	}
	var text string
	if err := json.Unmarshal(encoded, &text); err != nil {
		return ""
	}
	return text
}
