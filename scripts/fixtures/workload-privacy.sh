#!/bin/sh
set -eu

workspace="${1:?workspace is required}"
events_per_round="${2:?events per round is required}"
maximum_rounds="${3:?maximum rounds is required}"
measure_performance="${4:-0}"
quota_padding="hideout-quota-evidence-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

case "$events_per_round:$maximum_rounds" in
  *[!0-9:]* | :* | *:)
    echo "workload-privacy fixture: numeric bounds are required" >&2
    exit 2
    ;;
esac
case "$measure_performance" in
  0 | 1) ;;
  *)
    echo "workload-privacy fixture: performance mode must be 0 or 1" >&2
    exit 2
    ;;
esac

round=1
probe_requested=0
while [ "$round" -le "$maximum_rounds" ]; do
  if [ "$measure_performance" -eq 1 ]; then
    : >"$workspace/quota-start-$round"
    while [ ! -f "$workspace/quota-measure-$round" ]; do
      sleep 0.05
    done
    # Give the already-started host sampler one scheduling interval to attach
    # before the measured production observer load begins.
    sleep 0.2
    awk 'NR == 1 {print $1}' /proc/uptime \
      >"$workspace/quota-timing-start-$round"
  fi
  event=0
  while [ "$event" -lt "$events_per_round" ]; do
    # Keep each synthetic exec record large enough to cross the store's sealed
    # segment boundary in bounded time. The padding is public, fixed test data;
    # it is neither a secret nor derived from the host.
    /bin/true \
      "$quota_padding" "$quota_padding" \
      "$quota_padding" "$quota_padding"
    event=$((event + 1))
  done
  if [ "$measure_performance" -eq 1 ]; then
    awk 'NR == 1 {print $1}' /proc/uptime \
      >"$workspace/quota-ready-$round"
  else
    : >"$workspace/quota-ready-$round"
  fi

  while :; do
    if [ -f "$workspace/probe-go" ]; then
      probe_requested=1
      break
    fi
    if [ -f "$workspace/more-$round" ]; then
      break
    fi
    sleep 0.05
  done
  if [ "$probe_requested" -eq 1 ]; then
    break
  fi
  round=$((round + 1))
done

if [ "$probe_requested" -ne 1 ]; then
  echo "workload-privacy fixture: probe phase was not requested" >&2
  exit 1
fi

managed_secret="$(sed -n '1p' "$workspace/managed-secret.input")"
flag_secret="$(sed -n '1p' "$workspace/flag-secret.input")"
uri_value="$(sed -n '1p' "$workspace/uri.input")"
authorization_value="$(sed -n '1p' "$workspace/authorization.input")"
query_value="$(sed -n '1p' "$workspace/query.input")"
environment_only="$(sed -n '1p' "$workspace/environment-only.input")"
visible_name="$(sed -n '1p' "$workspace/visible-name.input")"

# File contents and environment values are deliberately handled by the target,
# but are never placed in an observed child argv.
/bin/cat "$workspace/content-only.input" >/dev/null
export HIDEOUT_PRIVACY_GATE_VALUE="$environment_only"
/bin/true

# These child argv forms exercise each deterministic pre-persistence rule.
/bin/echo "$managed_secret" >/dev/null
/bin/echo --token "$flag_secret" >/dev/null
/bin/echo "$uri_value" >/dev/null
/bin/echo "$authorization_value" >/dev/null
/bin/echo "$query_value" >/dev/null

# An ordinary local path must remain visible in the authenticated local view.
/bin/cat "$workspace/$visible_name" >/dev/null
: >"$workspace/probe-ready"

while [ ! -f "$workspace/loss-go" ]; do
  sleep 0.05
done

# These executions happen after the observer is killed. Their absence is not a
# no-behaviour claim: the terminal coverage must account for the known loss.
event=0
while [ "$event" -lt 128 ]; do
  /bin/true
  event=$((event + 1))
done
: >"$workspace/loss-done"

while [ ! -f "$workspace/release" ]; do
  sleep 0.05
done
