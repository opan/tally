#!/usr/bin/env bash
# Runs tally locally (Docker Compose, http://localhost:8090) with demo data:
# the made-up club Badminton Sampai Pingsan, four play days of points, and
# Iuran filled in. Log in as demo / demo1234. Running it again resets that
# club; other tallies in the local database are left alone.
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose up -d --build
printf 'Waiting for tally'
for _ in $(seq 90); do
  if curl -fsS -o /dev/null http://localhost:8090/; then break; fi
  printf '.'; sleep 1
done
echo
docker compose exec -T db psql -q -U tally -d tally -v ON_ERROR_STOP=1 < scripts/demo-seed.sql
echo "Open http://localhost:8090/t/sampai-pingsan and log in as demo / demo1234."
