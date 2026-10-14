#!/bin/sh
# Recreates the empty tally_guide database used by guide-shots.cjs.
set -e
cd "$(dirname "$0")/.."
docker compose exec -T db dropdb -U tally --if-exists tally_guide
docker compose exec -T db createdb -U tally tally_guide
