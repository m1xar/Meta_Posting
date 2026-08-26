# Campaign guards

Guards replace both Facebook automated rules and the previous free-form rule
DSL. A guard is a **ladder of spend checkpoints** attached to a batch at launch
time or to a single campaign later. The worker evaluates every active guard on
its own interval (default 5 minutes, `GUARD_EVALUATION_INTERVAL` for the
scheduler cadence).

## Checkpoint model

```json
{
  "checkpoints": [
    { "spend": 5,  "min_tracker_clicks": 20 },
    { "spend": 15, "min_tracker_leads": 1 },
    { "spend": 40, "min_tracker_sales": 1 }
  ]
}
```

Semantics:

- `spend` is **lifetime campaign spend** from the latest Facebook Insights
  snapshot, in the account currency. Checkpoints are sorted ascending and each
  `spend` value must be unique and positive.
- When lifetime spend reaches a checkpoint, every **non-zero** minimum on that
  checkpoint must already be met:
  - `min_clicks`, `min_impressions` — Facebook Insights;
  - `min_tracker_clicks`, `min_tracker_leads` (registrations),
    `min_tracker_sales` (deposits), `min_tracker_revenue` — Keitaro.
- A checkpoint with all minimums met is recorded as `passed` and never
  re-checked.
- A failed checkpoint pauses the campaign on Meta and records a `failed` check
  with the observed metrics and thresholds.
- At most 20 checkpoints per guard; every checkpoint needs at least one
  threshold.

## Scope and precedence

- A **batch guard** (created from the launcher) applies to every campaign the
  batch published.
- A **campaign guard** (created from the campaigns page) applies to exactly one
  campaign and shadows the batch guard for it.

## Manual overrides

Pausing and resuming from the campaigns page talk to Meta directly. Resuming a
campaign that a guard paused marks its `failed` checks as `overridden`, so the
guard does not immediately pause it again — but the **next** checkpoint on the
ladder still applies as spend grows.

Editing a live guard (PATCH `/app/api/guards/{id}`) replaces its checkpoint
ladder; already-passed checks are kept, and previously failed checks stay
overridden once a campaign was resumed by hand.

## Tracker matching

Keitaro statistics are matched to campaigns through the tracking link macros:

```
https://<tracker-domain>/?sub_id_3={{campaign.name}}&sub_id_7={{campaign.id}}&...
```

`sub_id_7` (campaign id) wins; `sub_id_3` (campaign name) is the fallback for
links that predate the id macro. Keep campaign names unique if you rely on the
fallback. The sync stores one all-time roll-up per campaign: clicks, unique
clicks, leads (registrations), sales (deposits), and revenue.
