# Tally

A points leaderboard for clubs and groups — start with a badminton season
ranking, extend to other sports or any tally. Admins enter players, shuffle
pairs, and record match scores; points add up per player and the ranking can be
shared as a link or a poster.

UI: Bahasa Indonesia (default) and English.

Plan, decisions, and backlog: [NOTES.md](NOTES.md).

## Run

```bash
docker compose up --build
```

Open <http://localhost:8090> and sign up. Postgres runs in compose (exposed on
`127.0.0.1:5433` for poking around); the schema is applied on app start.

Local Go against the compose database:

```bash
docker compose up -d db
DATABASE_URL=postgres://tally:tally@localhost:5433/tally?sslmode=disable go run .
```

## Test

```bash
go test ./...
```

Store tests run against a real Postgres when `TALLY_TEST_DATABASE_URL` points
at a throwaway database (it is wiped); otherwise they're skipped:

```bash
docker compose exec db createdb -U tally tally_test
TALLY_TEST_DATABASE_URL='postgres://tally:tally@localhost:5433/tally_test?sslmode=disable' go test ./...
```

## Configuration

| Env | Default | |
|---|---|---|
| `DATABASE_URL` | `postgres://tally:tally@localhost:5432/tally?sslmode=disable` | |
| `PORT` | `8080` | |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | enables OTLP metric export (see Metrics) |
| `OTEL_SERVICE_NAME` | `tally` | becomes the `job` label in VictoriaMetrics |
| `AUTH_PROVIDER` | `local` | `aio` = log in through All-in-one (below) |
| `AIO_ISSUER` | | aio's public base URL, e.g. `https://aio.example.com` |
| `AIO_CLIENT_ID` | | the client id registered in aio (`tally`) |
| `AIO_CLIENT_SECRET` | | printed once by `oidc:client:create`; keep it in a Secret |
| `AIO_REDIRECT_URL` | | `https://<tally host>/auth/callback` |
| `AIO_POST_LOGOUT_URL` | optional | `https://<tally host>/`, where aio sends the browser after logout |
| `AIO_INTERNAL_URL` | `AIO_ISSUER` | where tally asks aio, server to server, whether a user is a site admin; in the cluster use aio's Service, e.g. `http://all-in-one.app.svc.cluster.local` |

The `AIO_*` settings are required when `AUTH_PROVIDER=aio`; a missing one stops
tally at startup instead of silently falling back to local passwords.

## Login through All-in-one

With `AUTH_PROVIDER=aio`, tally never sees a password. **Masuk** and **Daftar**
go to aio's login and signup pages, which show tally's name, 🏆 icon, and gold
colour, in the language picked in tally (`ui_locales=id|en`). Someone already
logged in to aio goes straight through (single sign-on). **Keluar** ends both
the tally and the aio session. `POST /login` and `POST /register` redirect to
aio.

How it works (same as cashflow's `aioauth.go`): authorization code + PKCE +
nonce. State, nonce, and the PKCE verifier sit in a 10-minute `tally_oidc`
cookie. The ID token is verified (signature, issuer, audience, expiry, nonce),
then the local user is found by its aio id (`users.aio_user_id`) or created on
first login with aio's `preferred_username`. A username that already belongs to
a local-only tally account is never taken over (409 page). A username taken by
another aio account gets a suffix. If aio is down nobody new can log in, but
existing sessions keep working; tally also starts without aio.

Admins are added by username, so a new admin has to log in to tally once
before the owner can add them.

Register the client in aio (the secret is printed once):

```bash
kubectl -n app exec deploy/all-in-one -- /app/all-in-one oidc:client:create \
  --id tally --name Tally \
  --redirect-uri https://<tally host>/auth/callback \
  --post-logout-redirect-uri https://<tally host>/ \
  --brand-color '#d4af37' --icon 🏆
```

To try it locally, run aio from its repo with OIDC on
(`ALLINONE_SERVER_PORT=18080 ALLINONE_AUTH_OIDC_ENABLED=true
ALLINONE_AUTH_OIDC_ISSUER=http://localhost:18080`), register a client with
redirect URI `http://localhost:8091/auth/callback`, and run tally on the host
(`PORT=8091 DATABASE_URL=postgres://tally:tally@localhost:5433/tally?sslmode=disable
AUTH_PROVIDER=aio AIO_ISSUER=http://localhost:18080 ...`). The issuer must be
the same URL for the browser and for tally, which is why tally runs on the host
here rather than in Docker Compose.

### Site admin pages

`GET /admin` (every account, how many tallies they run, last activity) and
`GET /admin/users/{id}` (one account's tallies: role, visibility, players, play
days, matches, last played). Read-only, and only with `AUTH_PROVIDER=aio`. This
is the site admin, unrelated to a tally's own admins.

All-in-one decides who is a site admin (its `admin` group), on **every** admin
request (RFC-002 in all-in-one): tally calls `GET /api/v1/oidc/users/{sub}/status`
with `AIO_CLIENT_ID` and `AIO_CLIENT_SECRET`, with no cache, so someone removed
or blocked in aio loses access on their next request. Everyone else gets 404;
if aio can't be reached the pages answer 503 (fails closed) while the rest of
tally keeps working. The **Site admin** link in the menu uses the status at
login and is display-only. Private tallies are listed but not linked. Every
view is recorded in `admin_views` (admin, page, whose data).

## Metrics

Metrics are pushed over OTLP/HTTP every 15s when `OTEL_EXPORTER_OTLP_ENDPOINT`
is set, e.g. to an OpenTelemetry Collector that writes to Prometheus or
VictoriaMetrics (`prometheusremotewrite`):

```
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector.example:4318
OTEL_SERVICE_NAME=tally
```

Names as they appear in VictoriaMetrics / Grafana (label `job="tally"`):

| Metric | Type | Labels | What |
|---|---|---|---|
| `http_server_request_duration_seconds` | histogram | `http_route`, `http_request_method`, `http_response_status_code` | Request latency and counts |
| `http_server_active_requests` | gauge | | In-flight requests |
| `go_memory_used_bytes`, `go_goroutine_count`, … | gauge | | Go runtime |
| `tally_db_connections` | gauge | `state` = acquired, idle, max | Database pool |
| `tally_users`, `tally_tallies`, `tally_players`, `tally_matches`, `tally_play_days` | gauge | | Totals from the database (active players, finished matches) |
| `tally_tallies_active` | gauge | | Tallies with a play day in the last 30 days |
| `tally_users_created_total` | counter | `method` = local, aio | New accounts |
| `tally_tallies_created_total` | counter | `type` | New tallies, by preset |
| `tally_logins_total` | counter | `method`, `result` | Logins: `ok`, `failed` (wrong password), or the aio error (`state`, `denied`, `unavailable`, `exchange`, `verify`, `unlinked`) |
| `tally_play_days_started_total` | counter | | Play days started |
| `tally_match_events_total` | counter | `action` = scheduled, scored, added, edited, deleted, cancelled | Match activity |
| `tally_players_added_total`, `tally_awards_recorded_total` | counter | | Players added, extra-point entries |
| `tally_admin_checks_total` | counter | `result` = admin, not_admin, local_account, error | Site admin page requests by aio's answer (`error`: aio unreachable or unexpected reply) |

A ready-made Grafana dashboard for these metrics is kept as a Kubernetes
ConfigMap (`tally-dashboard`, for Grafana's dashboard sidecar) alongside the
deployment manifests, not in this repo.

## User guide

`/panduan` is the in-app guide (`/guide` redirects there). The text lives in
`templates/guide_id.html` and `templates/guide_en.html`, one per language, with
the same sections so links like `/panduan#hari-main` work in both. `go test`
checks that the sections match and that every screenshot exists.

The screenshots in `static/guide/{id,en}/` are generated, not hand-made. After
a UI change, regenerate them against an empty database:

```bash
./scripts/guide-db-reset.sh                       # empty tally_guide database
PORT=8095 DATABASE_URL='postgres://tally:tally@localhost:5433/tally_guide?sslmode=disable' go run . &
node scripts/guide-shots.cjs /path/to/node_modules/playwright-core
```

The script signs up a demo admin, seeds a fictional club (Badminton Sampai
Pingsan: Jacque Oui, Prabski, Bahlul…) with two play days, and captures each
screen at phone width in both languages.

## CI

GitHub Actions (`.github/workflows/`), with every third-party action pinned to
a commit:

| Workflow | When | What |
|---|---|---|
| `test.yml` | every push and PR | gofmt, vet, build, `go test -race` including the database tests (Postgres service) |
| `docker.yml` | push to `main`, `v*` tags | tests, build, Trivy image scan (fails on fixable HIGH/CRITICAL), push `<DOCKER_USERNAME>/tally` as `latest`, `sha-<short>`, and the version for tags |
| `security.yml` | every push and PR, weekly | gitleaks (whole history), govulncheck, gosec, Trivy (dependencies, secrets, Dockerfile) |

Needs the `DOCKER_USERNAME` and `DOCKER_PASSWORD` (a Docker Hub access token)
repository secrets. Scan results also appear under Security → Code scanning.
Dependabot (`.github/dependabot.yml`) opens weekly PRs for Go modules, base
images, and the pinned actions.

The image is distroless (`gcr.io/distroless/static-debian13:nonroot`): no
shell, runs as uid 65532.

## Tech

Go (single binary, `net/http`, `html/template`, embedded assets), PostgreSQL via
`pgx/v5`, cookie sessions (bcrypt for local accounts, or OpenID Connect through
All-in-one via `coreos/go-oidc` + `x/oauth2`), OpenTelemetry metrics. Same
shape as cashflow.

## Adding a language

Add `locales/<lang>.json` with the same keys as `locales/id.json` and append
the code to `supportedLangs` in `i18n.go`. `go test` fails if keys drift.
