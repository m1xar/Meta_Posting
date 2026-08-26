# Meta field coverage

## Why this exists

Meta's documentation drifts from the live API, and the Insights edge fails a
whole query with `(#100)` when a single field is invalid for the requested
level. That makes field selection a correctness problem, not a completeness
one: adding an unverified field does not degrade ingestion, it stops it.

So field sets here are verified against the API itself and pinned by tests.

## What is authoritative

- **Node fields** — every Graph object answers `GET /v25.0/<id>?metadata=1`
  with its own field list. That is the API's own answer and beats the docs.
- **Insights fields** — no `metadata=1` exists, but a rejection names the
  offending field. Submitting the full candidate set and removing whatever
  Meta names, until the query succeeds, converges on the accepted set.
  Validity varies by level, so each level is probed separately.

## Running the audit

`cmd/fieldaudit` is a development tool and is not built into the runtime
image: it needs a real token and one live object of each type.

```sh
go run ./cmd/fieldaudit \
  -token "$META_ACCESS_TOKEN" \
  -app-id "$META_APP_ID" -app-secret "$META_APP_SECRET" \
  -account 1234567890 \
  -campaign 23847... -adset 23848... -ad 23849... -creative 23850... \
  > docs/field-coverage-v25.md
```

`-account` alone probes just the Insights edge, which is the part that gates
ingestion. Add `-json` for machine-readable output.

Each probe iteration costs one request, so the candidate list is curated
(`ExtendedInsightFields`) rather than every string in the documentation.

## The three field sets

| Set | Used for | Status |
|---|---|---|
| `DefaultInsightFields` | every level | verified in production use |
| `AccountInsightFields` | account and campaign level | adds `account_currency`, `objective`, `buying_type` |
| `ExtendedInsightFields` | **candidates only** | unverified; the audit's input, never requested by default |

`ExtendedInsightFields` is deliberately never returned by `FieldsForLevel`.
`TestExtendedInsightFieldsAreNotUsedByDefault` enforces that, so a well-meaning
edit cannot promote an unverified field into the ingestion path.

`WindowedInsightFields` is separate and stays minimal: that query runs per
level per account nightly, and only reach, frequency and their denominators
cannot be derived from the daily rows.

## Changing a field set

1. Run the audit and confirm the level accepts the field.
2. Update the set in `internal/meta/insights.go`.
3. Update the golden list in `internal/meta/schemas_coverage_test.go` **in the
   same commit** — the test exists so a change is a diff to review, not a
   silent behaviour change.

## Typed versus raw

Spec structs carry a `Raw RawFields` escape hatch that is deep-merged over the
typed payload, so **no field is ever blocked** — anything Meta accepts can be
sent today.

Promoting a field to a typed struct field buys three things: validation,
visibility in `/v1/capabilities`, and a compile-time name. It also costs
maintenance at every API version. So the rule is: promote what the product
actually sets; leave the rest to `raw` and let the audit report it.

`FieldReport.Unknown` — modelled here but *not* reported by Meta — is the more
urgent direction to act on, since it usually means a field was renamed or
removed upstream.
