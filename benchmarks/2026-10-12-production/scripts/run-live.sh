#!/usr/bin/env bash
set -u
ulimit -n 65536
cd ~/k6bench
: "${SLUG:?set SLUG to a short link slug}" "${TARGET:?set TARGET to its exact target URL}"
OUT=results/$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$OUT"
ln -sfn "$(basename "$OUT")" results/latest
{ echo "host: $(nproc) cpus, $(free -h | awk '/Mem:/{print $2}') ram"; uptime; ./k6 version; } > "$OUT/env.txt"
for RATE in ${RATES:-1000 2000 3000}; do
  echo "=== $RATE r/s $(date -u +%T)" | tee -a "$OUT/progress.log"
  vmstat 5 17 > "$OUT/vmstat-$RATE.txt" &
  top -b -d 5 -n 17 | awk '$1 ~ /^[0-9]+$/ && $NF ~ /^(api|traefik|k6)$/ {print; fflush()}' > "$OUT/top-$RATE.txt" &
  ./k6 run --quiet --no-color -e BASE="${BASE:-https://shortened.cv}" -e SLUG="$SLUG" -e TARGET="$TARGET" -e RATE=$RATE \
    --summary-export "$OUT/summary-$RATE.json" redirect-live.js > "$OUT/k6-$RATE.log" 2>&1
  CODE=$?
  wait
  echo "exit=$CODE health=$(curl -s -o /dev/null -w '%{http_code}' ${BASE:-https://shortened.cv}/health/ready)" | tee -a "$OUT/progress.log"
  [ "$CODE" -ne 0 ] && { echo "stopped at $RATE r/s" | tee -a "$OUT/progress.log"; break; }
  sleep 30
done
echo "done $(date -u +%T)" | tee -a "$OUT/progress.log"
