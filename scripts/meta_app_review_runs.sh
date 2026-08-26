#!/usr/bin/env bash

set -Eeuo pipefail

# Run safe, read-only Marketing API calls for Meta App Review testing.
# The access token is read from the environment or from an interactive prompt;
# it is never written to disk or printed.

TARGET_SUCCESSES="${TARGET_SUCCESSES:-500}"
REQUEST_DELAY_SECONDS="${REQUEST_DELAY_SECONDS:-3}"
RATE_LIMIT_BACKOFF_SECONDS="${RATE_LIMIT_BACKOFF_SECONDS:-60}"
MAX_BACKOFF_SECONDS="${MAX_BACKOFF_SECONDS:-300}"
MAX_ATTEMPTS="${MAX_ATTEMPTS:-$((TARGET_SUCCESSES * 4))}"
GRAPH_API_VERSION="${GRAPH_API_VERSION:-v26.0}"
GRAPH_EDGE="${GRAPH_EDGE:-me/adaccounts}"
GRAPH_FIELDS="${GRAPH_FIELDS:-id,account_id,name}"

usage() {
  cat <<'EOF'
Usage:
  META_USER_ACCESS_TOKEN='...' APP_LABEL='Raze Posting' \
    ./scripts/meta_app_review_runs.sh

If META_USER_ACCESS_TOKEN is omitted, the script asks for it without echoing it.

Useful overrides:
  TARGET_SUCCESSES=500          Number of successful calls to reach
  REQUEST_DELAY_SECONDS=3       Delay after each successful call
  RATE_LIMIT_BACKOFF_SECONDS=60 Initial wait after HTTP 429 / Graph 80004
  MAX_BACKOFF_SECONDS=300       Maximum rate-limit backoff
  MAX_ATTEMPTS=2000             Safety limit for total HTTP attempts
  GRAPH_API_VERSION=v26.0       Graph API version
  GRAPH_EDGE=me/adaccounts      Read-only edge used for the run
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  usage
  exit 0
fi

if ! [[ "$TARGET_SUCCESSES" =~ ^[1-9][0-9]*$ ]]; then
  echo "TARGET_SUCCESSES must be a positive integer." >&2
  exit 2
fi

if ! [[ "$MAX_ATTEMPTS" =~ ^[1-9][0-9]*$ ]]; then
  echo "MAX_ATTEMPTS must be a positive integer." >&2
  exit 2
fi

APP_LABEL="${APP_LABEL:-Meta app}"
TOKEN="${META_USER_ACCESS_TOKEN:-}"
if [[ -z "$TOKEN" ]]; then
  read -r -s -p "Meta user access token for ${APP_LABEL}: " TOKEN
  printf '\n'
fi
if [[ -z "$TOKEN" ]]; then
  echo "An access token is required." >&2
  exit 2
fi

RESPONSE_FILE="$(mktemp -t meta-app-review.XXXXXX)"
cleanup() {
  rm -f "$RESPONSE_FILE"
}
trap cleanup EXIT

REQUEST_URL="https://graph.facebook.com/${GRAPH_API_VERSION}/${GRAPH_EDGE}"
successes=0
attempts=0
errors=0
rate_limit_errors=0
backoff_seconds="$RATE_LIMIT_BACKOFF_SECONDS"

printf 'Starting %s: target=%s successful calls, delay=%ss, edge=%s\n' \
  "$APP_LABEL" "$TARGET_SUCCESSES" "$REQUEST_DELAY_SECONDS" "$GRAPH_EDGE"

while (( successes < TARGET_SUCCESSES )); do
  if (( attempts >= MAX_ATTEMPTS )); then
    echo "Stopped at the safety limit of ${MAX_ATTEMPTS} total attempts." >&2
    break
  fi

  attempts=$((attempts + 1))
  http_code="$(curl --silent --show-error --location --get "$REQUEST_URL" \
    --connect-timeout 15 --max-time 45 \
    --data-urlencode "fields=${GRAPH_FIELDS}" \
    --data-urlencode 'limit=1' \
    --config <(printf 'header = "Authorization: Bearer %s"\n' "$TOKEN") \
    --output "$RESPONSE_FILE" --write-out '%{http_code}' 2>/dev/null || true)"

  graph_error_code=""
  if grep -q '"error"' "$RESPONSE_FILE"; then
    graph_error_code="$(sed -nE 's/.*"code"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p' "$RESPONSE_FILE" | head -n 1)"
  fi

  if [[ "$http_code" =~ ^2[0-9][0-9]$ ]] && [[ -z "$graph_error_code" ]]; then
    successes=$((successes + 1))
    backoff_seconds="$RATE_LIMIT_BACKOFF_SECONDS"
    if (( successes % 25 == 0 || successes == TARGET_SUCCESSES )); then
      printf 'Progress: %s/%s successful, %s attempts, %s errors\n' \
        "$successes" "$TARGET_SUCCESSES" "$attempts" "$errors"
    fi
    sleep "$REQUEST_DELAY_SECONDS"
    continue
  fi

  errors=$((errors + 1))
  if [[ "$http_code" == "429" || "$graph_error_code" == "80004" ]]; then
    rate_limit_errors=$((rate_limit_errors + 1))
    printf 'Rate limit after attempt %s; sleeping %ss before retry.\n' \
      "$attempts" "$backoff_seconds" >&2
    sleep "$backoff_seconds"
    next_backoff=$((backoff_seconds * 2))
    if (( next_backoff > MAX_BACKOFF_SECONDS )); then
      backoff_seconds="$MAX_BACKOFF_SECONDS"
    else
      backoff_seconds="$next_backoff"
    fi
  else
    printf 'Unsuccessful attempt %s: HTTP %s%s; sleeping %ss.\n' \
      "$attempts" "$http_code" \
      "${graph_error_code:+, Graph error ${graph_error_code}}" \
      "$REQUEST_DELAY_SECONDS" >&2
    sleep "$REQUEST_DELAY_SECONDS"
  fi
done

success_percent="$(awk -v s="$successes" -v a="$attempts" \
  'BEGIN { if (a == 0) printf "0.00"; else printf "%.2f", (100 * s / a) }')"

printf '\nCompleted %s: successful=%s, attempts=%s, errors=%s, rate_limits=%s, success_rate=%s%%\n' \
  "$APP_LABEL" "$successes" "$attempts" "$errors" "$rate_limit_errors" "$success_percent"

if (( successes < TARGET_SUCCESSES )); then
  echo "Target was not reached." >&2
  exit 1
fi

if awk -v p="$success_percent" 'BEGIN { exit !(p < 85) }'; then
  echo "The run reached its call target but is below Meta's 85% success threshold." >&2
  exit 1
fi

echo "Run completed at or above the 85% success threshold."
