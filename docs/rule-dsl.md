# Automation rule DSL

Automation is intentionally pause-only. A rule evaluates one Meta entity at
`campaign`, `adset`, or `ad` level and may set that entity to `PAUSED`. Rules do
not resume objects or change budgets/bids.

## Timing and eligibility

Each rule has:

- `lookback_seconds`: rolling Insights window;
- `evaluation_interval_seconds`: minimum time between scheduled checks;
- `grace_period_seconds`: time after the local published-object record's
  `created_at` during which no action is taken;
- `cooldown_seconds`: minimum delay after a prior trigger before another action;
- `minimum_spend` and `minimum_impressions`: top-level sample gates;
- `timezone`: IANA timezone, normally the ad account's timezone;
- optional per-rule, per-group, and per-condition `minimum_samples` guards.

The current grace-period clock starts at the service's local launch/create
record, not at Meta's first effective `ACTIVE` timestamp. The worker interval
controls how often due rules are discovered and does not guarantee an
evaluation at an exact wall-clock second.

## Boolean expression

`conditions` is a recursive group:

```json
{
  "logic": "all",
  "conditions": [
    {"metric": "spend", "operator": "gte", "threshold": 100},
    {
      "metric": "actions.complete_registration",
      "operator": "lt",
      "threshold": 1,
      "missing_as_zero": true
    }
  ],
  "groups": [
    {
      "logic": "any",
      "conditions": [
        {"metric": "ctr", "operator": "lt", "threshold": 0.5},
        {"metric": "cpc", "operator": "gt", "threshold": 5}
      ]
    }
  ]
}
```

`all` requires every direct condition and child group to match. `any` requires
at least one. A group must contain at least one condition or child group.

Numeric operators:

| Operator | Meaning |
|---|---|
| `gt` | observed value is greater than threshold |
| `gte` | observed value is greater than or equal |
| `lt` | observed value is less than threshold |
| `lte` | observed value is less than or equal |
| `eq` | observed value equals threshold |
| `neq` | observed value does not equal threshold |

## Metrics

Common rule metric names include:

- `spend`, `impressions`;
- `clicks`, `unique_clicks`, `inline_link_clicks`;
- `ctr`, `cpc`, `cpm`;
- flattened action, conversion, value, cost, ROAS, outbound-click, and video
  metrics.

`reach`, `frequency`, and `cpp` remain available in stored lifetime snapshots,
but are not exposed to rolling-window rules. Reach is a unique count and cannot
be recovered by subtracting two lifetime snapshots; frequency and CPP depend on
that non-additive count. Lifetime-only averages such as
`video_avg_time_watched_actions.*` are likewise retained in raw snapshots but
not used for rolling automation.

Meta action/value arrays are flattened to dotted names. Examples:

- `actions.complete_registration`;
- `actions.purchase`;
- `conversions.custom_conversion_id`;
- `action_values.purchase`;
- `cost_per_action_type.complete_registration`;
- `purchase_roas.omni_purchase`.

The exact dotted keys depend on the data returned for an account and attribution
setting. Query Insights and inspect each snapshot's `metrics` object before
activating a rule. The snapshot also exposes convenience aggregate fields such
as `registrations`, `purchases`, and `roas`; the DSL evaluates the keys in
`metrics`, so use the dotted event key shown there.

Metric names are lower-case dotted identifiers. Each segment starts with a
letter or digit and may contain letters, digits, `_`, or `-`.

## Missing metrics

Missing data does not normally satisfy a condition. Set `missing_as_zero: true`
only when absence semantically means zero, such as a conversion action that has
not occurred. Do not set it for ratios or costs whose denominator may be
missing.

Example guard:

```json
{
  "metric": "roas",
  "operator": "lt",
  "threshold": 0.8,
  "minimum_samples": [
    {"metric": "spend", "minimum": 100},
    {"metric": "purchases", "minimum": 1}
  ]
}
```

This avoids interpreting absent ROAS as a real zero before any purchase is
recorded.

## Rolling windows and cumulative counters

Rules compare metrics for their configured rolling window. When the worker uses
cumulative snapshots, it subtracts an older snapshot from the newest one.
Before evaluating conditions, the newest snapshot must be no more than two
configured `INSIGHTS_POLL_INTERVAL` collection intervals old. This fixed,
bounded two-interval allowance tolerates one delayed collection without letting
an automation action use indefinitely stale performance data. A stale snapshot
creates a `skipped` evaluation and never attempts a pause.
If the closest baseline is farther from the requested start than one collection
interval, evaluation is skipped. A cumulative counter that decreased is treated
as a reporting correction and also skips evaluation; it is never interpreted as
a counter reset or as current-window performance. The same correction rule
applies when a previously reported additive key (for example,
`actions.purchase` or a conversion key) disappears from the newest lifetime
snapshot; omitted counters are not silently converted into zero-performance
signals.

Meta attribution and reporting delays mean a 24-hour window is not necessarily
final at the instant it closes. Choose grace periods, minimum samples, and
thresholds accordingly.

## Safe rollout and evaluation audit

Create a new rule with `status: "disabled"`, review its metrics and sample
gates, and enable it with `POST /v1/rules/{id}/enable`. Disable it with
`POST /v1/rules/{id}/disable`.

Every scheduled evaluation is auditable through
`GET /v1/rules/{id}/evaluations`: the persisted record contains the window,
observed metrics, individual condition outcomes, whether an action was
attempted, the action response, and any error.

## Suggested iGaming starting presets

These are conservative templates, not universal thresholds. Replace the money
values with targets in each ad account's currency, verify the exact event keys
in stored Insights, and start disabled.

### Spend without registration

Campaign or ad-set level, 24-hour window:

```json
{
  "lookback_seconds": 86400,
  "evaluation_interval_seconds": 900,
  "grace_period_seconds": 21600,
  "minimum_spend": 100,
  "minimum_impressions": 1000,
  "conditions": {
    "logic": "all",
    "conditions": [
      {
        "metric": "actions.complete_registration",
        "operator": "lt",
        "threshold": 1,
        "missing_as_zero": true
      }
    ]
  }
}
```

### Registration CPA over target

Campaign or ad-set level, 24-hour window, with at least three observed
registrations:

```json
{
  "lookback_seconds": 86400,
  "minimum_spend": 100,
  "conditions": {
    "logic": "all",
    "conditions": [
      {
        "metric": "cpa.complete_registration",
        "operator": "gt",
        "threshold": 40,
        "minimum_samples": [
          {"metric": "actions.complete_registration", "minimum": 3}
        ]
      }
    ]
  }
}
```

### Purchase ROAS below floor

Campaign level, three-day window, only after enough spend and purchases:

```json
{
  "lookback_seconds": 259200,
  "minimum_spend": 300,
  "conditions": {
    "logic": "all",
    "conditions": [
      {
        "metric": "roas.purchase",
        "operator": "lt",
        "threshold": 0.8,
        "minimum_samples": [
          {"metric": "actions.purchase", "minimum": 3}
        ]
      }
    ]
  }
}
```

### Low-CTR creative

Ad level, 24-hour window:

```json
{
  "lookback_seconds": 86400,
  "minimum_impressions": 3000,
  "conditions": {
    "logic": "all",
    "conditions": [
      {"metric": "ctr", "operator": "lt", "threshold": 0.6}
    ]
  }
}
```

Meta reporting is attribution-delayed. For conversion/ROAS presets, a longer
grace period and several required outcomes are safer than reacting to the first
few hours of spend.
