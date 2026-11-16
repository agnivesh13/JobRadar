#!/usr/bin/env bash
# Chaos test: kill containers while a full crawl is running, then prove that
# no job was lost and none was duplicated.
#
#   bash scripts/chaos.sh [kills] [seconds-between-kills]
#
# What it does:
#   1. asks the scheduler to re-crawl every company (re-extraction forced)
#   2. SIGKILLs one container at a time, in rotation, and starts it again:
#      fetchers, extractors, the scorer, both schedulers, NATS and Postgres
#   3. waits for the queue to drain
#   4. runs `jobradar verify`, which compares the database against the stored
#      raw feeds (see cmd/jobradar/verify.go for the exact invariants)
#
# Exit code 0 means every invariant held.

set -u
cd "$(dirname "$0")/.."

KILLS="${1:-14}"
GAP="${2:-7}"
DC="docker compose"
tool() { $DC run --rm -T --no-deps migrate "$@" 2>&1 | grep -v -e '^ Container' -e 'Creating' -e 'Created'; return "${PIPESTATUS[0]}"; }
sql() { $DC exec -T postgres psql -U jobradar -d jobradar -At -c "$1"; }

VICTIMS=(fetcher-1 extractor-1 scorer-1 scheduler-1 fetcher-2 nats-1 extractor-2 scheduler-2 fetcher-3 postgres-1 scorer-1 fetcher-1 extractor-1 scheduler-1)

echo "== starting stack"
$DC up -d >/dev/null 2>&1 || { echo "docker compose up failed"; exit 1; }
sleep 5

before_jobs=$(sql "select count(*) from jobs")
companies=$(sql "select count(*) from companies where status='active'")
echo "== before: $companies active companies, $before_jobs job rows"

echo "== requesting a full re-crawl"
tool recrawl || exit 1

echo "== chaos: $KILLS kills, one every ${GAP}s"
for i in $(seq 1 "$KILLS"); do
  victim="jobradar-${VICTIMS[$(( (i - 1) % ${#VICTIMS[@]} ))]}"
  sleep "$GAP"
  done_n=$(sql "select count(*) from companies where status='active' and not force_next and last_crawled_at > now() - interval '15 minutes'" 2>/dev/null || echo "?")
  echo "   kill $i/$KILLS: $victim   (companies finished so far: $done_n/$companies)"
  docker kill "$victim" >/dev/null 2>&1 || echo "   (could not kill $victim)"
  sleep 2
  docker start "$victim" >/dev/null 2>&1 || echo "   (could not start $victim)"
done

echo "== waiting for the queue to drain"
deadline=$(( $(date +%s) + 900 ))
while :; do
  sleep 10
  if out=$(tool verify); then
    echo "$out"
    after_jobs=$(sql "select count(*) from jobs")
    echo "== after: $after_jobs job rows (was $before_jobs)"
    echo "== CHAOS TEST PASSED"
    exit 0
  fi
  if [ "$(date +%s)" -gt "$deadline" ]; then
    echo "$out"
    echo "== CHAOS TEST FAILED (invariants still broken after 15 minutes)"
    exit 1
  fi
  echo "   not settled yet: $(echo "$out" | grep -c FAIL) open issues, e.g. $(echo "$out" | grep -m1 FAIL)"
done
