# Tally — Plan & Notes

Living plan + backlog so we don't lose context between sessions.

_Last updated: 2026-10-18 (P8 site admin)_

> **Status 2026-10-14:** aio's OIDC is live (opan tested it with cashflow) and
> tally's aio login is built and tested end to end against a local aio. Now:
> DOC (user guide, first version, opan revises later). **opan does the
> deployment themselves** (homelab k8s, `AUTH_PROVIDER=aio`); README has the env
> vars and the aio client command. Security backlog still open. Runs locally with `docker compose up` (port 8090,
> local passwords).

---

## What it is

A web app for running a points leaderboard ("tally") for a group: a badminton
club's season ranking first (e.g. *Bala Bulu Point Rank*), but generic enough for
other sports and non-sport tallies. The output people care about is the
leaderboard and a shareable poster of it (dark/gold, top‑3 highlighted, movement
arrows).

UI is **Bahasa Indonesia by default, switchable to English** (all strings are
localized; see `locales/`).

## Decisions (agreed 2026-10-13)

| Topic | Decision |
|---|---|
| Stack | Same as cashflow: Go single binary, `net/http`, `html/template`, vanilla JS, Postgres (`pgx/v5`), `go:embed`, OTel metrics, k8s in homelab |
| Users | Simple local username/password now. `users.aio_user_id` exists from day one so `AUTH_PROVIDER=aio` (port cashflow's `aioauth.go`) needs no migration |
| Who uses it | **Admins** (registered users) run a tally: enter player names, shuffle pairs, enter scores. A tally can have several admins |
| Players | Just names typed in by an admin (`participants`), not accounts. Optional link to a user later |
| Corrections | Matches can be edited and deleted; every change is written to `audit_log` |
| Format | Fixed **per period (season)** |
| Ranking | Total points (attendance naturally helps — it's open play, intended). Board also shows matches played + average; the poster can hide them |

## Scoring

Stored as JSON on the period:

```json
{ "format": "continuous", "target": 42, "games": 2, "game_target": 21, "win_bonus": 0 }
```

- **continuous** — one running score, race to `target` (42). Input: final score per side.
- **reset** — `games` (2) games, each to `game_target` (21), score resets per game.
  Input: each game's score per side. Side total = sum of its games.
- **Points per player** = their side's total + `win_bonus` if their side won.
- **Winner**:
  - continuous: higher score. Ties are rejected.
  - reset: side that won more games. 1–1 is a **draw** → nobody gets the bonus.
    Individual games can't be tied.
- Validation is deliberately loose (non‑negative, game scores not tied, sane
  upper bound). Deuce scores (23‑21, 30‑29, 43‑41) and **unfinished matches**
  (court time ran out at 38‑35) are accepted.
- Every player on a side gets the same points. Pairs are reshuffled each match,
  so points belong to individuals, never to pairs.
- Points are computed when a match is saved and written to `point_awards`
  (ledger). Editing a match rewrites its awards. Manual adjustments are awards
  without a match. Changing a period's rule offers "recalculate period".

**Later:** a 3rd (rubber) game where winning matters → `"games": "best_of_3"`.
Scores are already stored as a list of games, so no migration.

Example (continuous, win_bonus 0): A+B vs C+D, 42‑36 → A 42, B 42, C 36, D 36.
Example (reset): 21‑9, 21‑19 → A/B 42, C/D 28. 21‑19, 18‑21 → draw, 39 / 40.

## Data model

```
users(id, username, password_hash NULL, aio_user_id NULL)   sessions(...)
tallies(id, owner_id, slug UNIQUE, name, description, template, visibility)
tally_admins(tally_id, user_id, role owner|admin)
participants(id, tally_id, display_name, active, user_id NULL)
periods(id, tally_id, name, starts_on, ends_on, scoring jsonb)
rounds(id, period_id, number, label, played_on)              -- "Week #2 – 11 Oct"
matches(id, period_id, round_id, played_on, notes, created_by, updated_by)
match_sides(match_id, side, games int[], total, result win|draw|loss)
match_players(match_id, side, participant_id)
point_awards(id, period_id, participant_id, match_id NULL, points, reason)
audit_log(id, tally_id, actor_id, action, entity, entity_id, before, after)
```

Leaderboard = `SUM(point_awards.points)` per participant in a period, up to a
given round. Movement arrow = rank after round N vs rank after round N‑1.

## Screens / journey

1. Register / log in (local).
2. Create tally: name, slug (`/t/{slug}`), template, visibility.
3. Add players: paste names, one per line.
4. Create period: "Season #1", 1 Oct – 31 Dec 2026, scoring rule.
5. **Match day**: tick who's present, pick courts (1–3) → **Shuffle pairs**
   (fair rotation, random pairs) → enter scores per court.
6. Leaderboard `/t/{slug}` with period + round switcher, matches played, average.
7. Share poster in the dark/gold style + **Download PNG** (vendored html‑to‑image, CSP‑safe).
8. Player page: history, points over time, partners/opponents.

## Phases

| Phase | Scope | Status |
|---|---|---|
| P0 | Scaffold, schema, local auth, i18n (id/en), scoring engine + tests, Docker Compose | ✅ done 2026-10-13 |
| P1 | Tally / admins / players / periods CRUD, badminton template | ✅ done 2026-10-13 |
| P2 | Match day: attendance, shuffle pairs, score entry, point ledger, edit/delete + audit | ✅ done 2026-10-14 |
| P3 | Leaderboard: season + play-day switcher, movement arrows, play-day results, public view | ✅ done 2026-10-14 |
| P4 | Share poster + PNG export, optional logo & texts | ✅ done 2026-10-14 |
| P5 | More tally types (1v1, 2v2, team, counter), win/draw/loss scoring, manual points, player pages | ✅ done 2026-10-14 |
| DOC | User guide: getting started + how to use each feature, in the app (`/panduan`), ID + EN | first version 2026-10-14; opan to review on a phone and revise |
| P6 | Best‑of‑3 / rubber game where winning matters | later |
| P7 | Login through aio (`AUTH_PROVIDER=aio`) | ✅ done 2026-10-14 (linking players to accounts: later) |
| P8 | Read-only site admin pages, admin decided by aio (RFC-002 in all-in-one) | ✅ built 2026-10-18, PR #2 |
| P9 | Forgot / change password and email links to aio (RFC-003 in all-in-one) | ✅ built 2026-10-27, waits for all-in-one#36 |
| — | k8s deploy in `homelab/k8s/tally`, domain | when P3 is usable |

## P1 notes (2026-10-13)

Routes: `POST /tallies`; `/t/{slug}` (overview, public unless private);
admin-only `/t/{slug}/players`, `/periods`, `/periods/{id}`, `/settings`;
owner-only `POST /t/{slug}/admins`, `/admins/{uid}/remove`.

- Presets: `badminton_doubles` (2 per side), `badminton_singles` (1). Both
  default to continuous‑to‑42. Link (slug) and preset are fixed after creation.
- Visibility offered: **link** (anyone with the link, default) or **private**
  (admins only; looks like a 404 to others). `public` exists in the schema
  for a future directory but isn't offered.
- Player paste accepts a chat roll call: strips `1.` / `2)` / `-` / `•`,
  splits on newlines and commas, dedupes case-insensitively, skips existing.
- Players with matches can't be deleted (FK) → deactivate instead.
- New season defaults: next number, starts the day after the latest season
  ends (or today), 3 months, copies the latest season's rule.
- Every create/update/delete writes `audit_log` (no UI yet).
- Not built: deleting a tally, transferring ownership, audit-log viewer.

## P2 notes (2026-10-14)

Routes (admin): `/t/{slug}/play` (jumps to today's play day if it exists;
`?new=1` to start another), `POST /t/{slug}/rounds`, `/rounds/{rid}`,
`POST .../setup` (attendance + courts), `POST .../shuffle`,
`POST .../matches` (manual), `/matches/{mid}/edit`, `POST /matches/{mid}`
(`action=save|delete`).

- **Round = play day**, one per date per season, numbered by date order
  within the season ("Minggu #N" = Nth play day, not calendar week).
  Courts 1–3 per play day, changeable.
- **Match lifecycle:** shuffle creates `scheduled` matches (on court, no
  score). Saving a score makes it `done` and writes `match_sides` +
  `point_awards`. Cancel deletes a scheduled match; delete removes a done one
  and its points. Manual entry creates a `done` match directly.
- **Shuffle** (`shuffle.go`): fills free courts only, from players who are
  present, active, and not on court. Order: fewest matches today → waited
  longest → random. Pairing among the chosen is purely random (decided
  2026-10-14). Tested to keep match counts within 1 of each other.
- **Season edits** after scored matches: format/games locked; other rule
  changes (win bonus) rescore every match; dates must still cover all play days.
- Overview shows a basic standings table (rank, points, played, avg).
- Manual point adjustments were added in P5. Still not built:
  deleting a play day.

## P3 notes (2026-10-14)

- `/t/{slug}?season=<id>&day=<id>`: board for a season as of a play day;
  defaults to the current season and its latest play day with scored matches.
  Unknown ids fall back to the defaults.
- **Ranking** (`board.go`): points desc → fewer matches played → name.
  **Ties never share a rank** (decided 2026-10-14).
- **Arrows** compare rank after the selected play day with rank after the
  previous play day that had scores: ↑n / ↓n / ↔, and "baru" for a player's
  first appearance. The season's first play day shows ↔ for everyone.
- Below the board: that play day's results (public, read-only).
- Avg column hidden on phones.

## P4 notes (2026-10-14)

- `/t/{slug}/poster?season=&day=`: same visibility as the board. A fixed
  1024×1536 canvas (`.poster`), scaled to the screen by `static/poster.js`.
- **Export** is client-side with vendored `html-to-image` 1.11.13 (MIT,
  `static/vendor/`), so the CSP stays `'self'`-only. `base-uri` is `'self'`
  (was `'none'`) because the library sets a same-origin `<base>`. Safari
  renders twice (known first-render font/image drop). **Bagikan** (Web Share
  with files) appears only where the browser supports it, e.g. phones →
  WhatsApp.
- **Font**: Exo 2 (OFL, `static/fonts/`), latin subset, used only on the poster.
- **Branding** (Setelan → Poster), all optional (decided 2026-10-14):
  title line 1 (white) / line 2 (gold) — default splits the tally name;
  badge text under the logo; footer text; logo upload.
- **Logo**: PNG/JPEG/WebP only (SVG refused: can carry script), ≤1 MiB,
  ≤4096px a side, must decode. Stored in `tally_logos` (bytea), served at
  `/t/{slug}/logo?v=<updated unix>` with immutable caching.
- Badminton presets draw a shuttlecock emblem when there's no logo.
- Text fitting is measured in the browser: titles shrink to clear the logo,
  badge shrinks to its box, row height scales so up to 20 players fill the
  table (more than 20 → top 20).

## P5 notes (2026-10-14)

- **Presets** (`presets.go`), each with a kind and allowed score formats:
  `badminton_doubles`/`_singles` (continuous, reset), `duel` 1v1 and `pairs`
  2v2 (outcome, continuous, reset), `team` (outcome…, players per side 3–11
  chosen at creation → `tallies.side_size`), `counter` (no matches).
- **Outcome format**: one final score, equal = draw, points by result
  (default win 3 / draw 1 / loss 0). Score is recorded, points ignore it.
- **Manual points** (`point_awards` with `match_id` NULL): grid of present
  players (or all active if nobody ticked) with a number each + shared note;
  negative = penalty. On match tallies it's the "Poin tambahan" card; on
  counter tallies it's the whole play day. Deletable; audited.
- `point_awards.round_id` now set on every award (backfilled), so standings
  "as of" a play day include manual points. A play day counts for the board
  once it has any award.
- Counter boards show **Catatan** (entries) instead of matches played.
- **Player page** `/t/{slug}/players/{pid}?season=` (public like the board;
  names on the board link to it): rank, points, played, W–D–L, avg; points and
  rank after each play day; partners and opponents (played/won); match
  history; manual entries.

## Bug fixes (2026-10-14)

- **Couldn't delete a player who never played**: `round_attendance` blocked
  the delete (FK without cascade) and the app reported "already has matches".
  Attendance now cascades (schema upgrades the constraint in place).
  Regression test: `TestDeletePlayerWhoOnlyAttended` (needs
  `TALLY_TEST_DATABASE_URL`).
- **No way to cancel** the manual-match form (and the extra-points form /
  player edit panel): added **Batal**, which clears and collapses it.
- **Collapsible sections had no affordance**: chevron ▸/▾ on every
  collapsible header and on "Tambah pertandingan manual".

## User guide plan (DOC, added 2026-10-14)

Goal: a new admin can go from sign-up to a shared poster without help, and
players opening the link understand the board.

**Format**
- In-app page at `/panduan` (like cashflow's), public, linked from the
  header/footer and from empty states (no tallies, no players, no season,
  empty board). Deep links per section (`/panduan#main`) so "?" links next to
  features open the right part.
- Text in `locales/` like the rest of the UI → Indonesian default, English
  via the same toggle; `go test` keeps both complete.
- Screenshots generated, not hand-made: a Playwright script seeds a demo
  tally (fictional names: Badminton Sampai Pingsan, Jacque Oui, Prabski,
  Bahlul…) and captures phone-width shots into `static/guide/`, so the guide
  can be refreshed whenever the UI changes. Optional: a one-page printable
  PDF/PNG quick-start for the club's WhatsApp group.

**Outline**
1. *Mulai cepat* — five steps: daftar → buat tally (pilih jenis) → tulis
   nama pemain → buat musim & aturan poin → mulai main.
2. *Jenis tally* — bulu tangkis ganda/tunggal, 1 lawan 1, 2 lawan 2, tim,
   hitungan bebas: when to use which; team size is fixed at creation.
3. *Aturan poin* — skor berlanjut (42), 2 gim diulang (21×2, 1-1 = seri),
   menang/seri/kalah, bonus menang; worked examples (42‑36 → A/B 42,
   C/D 36); what can't change once a season has matches and what rescores.
4. *Hari main* — hadir & lapangan, **Acak pasangan** (who goes first and
   why), ganti pemain, simpan skor, batalkan, pertandingan manual, poin
   tambahan (bonus/penalti), late arrivals / early leavers.
5. *Papan peringkat* — how ranking & ties work, arrows ↑↓↔ and "baru",
   "per hari main" picker, player pages.
6. *Poster* — making and sharing (Unduh / Bagikan), setting title, logo,
   badge and footer text.
7. *Mengelola* — pemain (ganti nama, nonaktifkan vs hapus), musim baru,
   admin lain, siapa yang bisa melihat (tautan / hanya admin).
8. *Untuk pemain* — short section for people who only open the link.
9. *FAQ* — "kenapa pemain tidak bisa dihapus?", "salah input skor?",
   "ganti format di tengah musim?", "poster gagal di iPhone?".

**Done when**: every feature on the tabs is covered; screenshots match the
current UI; ID and EN complete; reviewed by opan on a phone.

**Built (2026-10-14, first version):** `/panduan` (+ `/guide` redirect),
all 9 sections in ID and EN, 15 screenshots per language from
`scripts/guide-shots.cjs`. Linked from the footer, landing, empty dashboard,
the tally setup checklist, the season form (`#aturan-poin`), and the shuffle
help (`#hari-main`). The sign-up step adapts to `AUTH_PROVIDER=aio`.
Change from the plan: the guide's prose lives in `templates/guide_{id,en}.html`
rather than locale keys (long text is easier to revise there); only the page
chrome uses `locales/`. Not done yet: printable quick-start, opan's review.

## CI and security scans (2026-10-14)

Workflows: test (race + Postgres service), docker (test gate → build → Trivy
image gate → push to Docker Hub, opan added the secrets), security (gitleaks,
govulncheck, gosec, Trivy fs; weekly too), Dependabot. All actions pinned to
commit SHAs. Ran every scanner locally first: govulncheck showed the old build
image (Go 1.26.6) had reachable net/http vulnerabilities fixed in 1.26.9 → build
image `golang:1.26.9-alpine` and `toolchain go1.26.9` in go.mod; gosec G404 on
the shuffle RNG → ChaCha8 seeded from crypto/rand (+ `#nosec` with reason).
Runtime image moved from alpine:3.20 to distroless static (Debian 13, nonroot
uid 65532, 39 MB); Trivy: 0 fixable HIGH/CRITICAL. Remaining notice:
GO-2026-5932 (x/crypto/openpgp, unmaintained), not imported by tally.
Moved to Go 1.27.2 (Dependabot PR #1, merged 2026-10-14): build image, go.mod
`toolchain`, and CI `go-version: '1.27.x'` move together; `go 1.26.0` language
line unchanged. Re-ran race tests, govulncheck and Trivy on 1.27.2: clean.
gosec v2.29.0 (latest) bundles x/tools v0.49.0, which can't read Go 1.27
export data ("export data version 5"), so CI builds it against x/tools v0.52.0;
drop that step once a gosec release bumps x/tools.

## Double-submit protection (2026-10-14)

Two layers, as in cashflow. **Browser** (`static/app.js`): every POST form
locks on submit (after `data-confirm`); the pressed button shows a spinner and
"Menyimpan…" / "Mengunggah…" (file attached); it unlocks after 20s with no
answer and on bfcache restore. **Server** (`submitguard.go`): one-time
`form_token` on the forms that create records (new tally, new season, manual
match, extra points); a duplicate redirects to where the first landed; a
failed submission releases its token. In-memory, so per pod and lost on
restart; a DB-backed guard if that ever matters. Other forms were already
harmless to repeat (upserts, dedupes, or an error message). Tested in a
browser: double/triple taps → one request and one record; a replayed token
→ no second record.

## Metrics (2026-10-14)

App metrics added on top of HTTP + runtime: DB totals as gauges (users,
tallies, active tallies 30d, players, matches, play days, DB pool) and event
counters (logins by method/result, users/tallies created, play days, match
events by action, players added, extra points). Names and labels: README →
Metrics. Verified end to end locally through otel-collector-contrib
(prometheusremotewrite) → VictoriaMetrics → Grafana 13. Dashboard: ConfigMap
`tally-dashboard` (sidecar label `grafana_dashboard: "9"`) in
`homelab/k8s/observabilities/vm/dashboards/`, same layout as cashflow's plus an
"Aplikasi" section. Failed logins are on it because login rate limiting is
still in the security backlog.

## P7 notes: login through aio (2026-10-14)

- Port of cashflow's `aioauth.go` (same decisions: code + PKCE + nonce, link by
  `sub`, never take over a local-only username, fail closed, lazy discovery,
  logout ends the aio session, CSP `form-action` lists aio's origin). Also took
  cashflow's stricter `safeNext` (tabs/newlines/backslashes).
- Differences: errors are localized (`auth.err.*`, `autherror` page);
  `ui_locales` follows tally's language cookie instead of always `id`; cookie
  `tally_oidc`; new aio users count in `tally.users.created`.
- aio branding for the client: `--brand-color '#d4af37' --icon 🏆`.
- No `users:import` needed: tally has never been deployed, so production starts
  on `AUTH_PROVIDER=aio` with no local accounts. Local passwords stay for dev.
- Admins are added by username, so a new admin must log in to tally once first
  (message `err.admin_not_found` says so).
- Tested end to end with Playwright against a local aio (sqlite, port 18080) and
  tally on the host (port 8091): signup → back logged in, logout ends both
  sessions (next login shows aio's form, not SSO), login with `?next=` in
  English, and an aio `qa_owner` colliding with the local `qa_owner` → 409 page.

## P8 notes: site admin pages (2026-10-18)

- Design: all-in-one RFC-002 / ADR-O10. On **every** request to `/admin` and
  `/admin/users/{id}`, tally asks aio `GET /api/v1/oidc/users/{sub}/status` with
  the OIDC client id + secret, no cache, so removal or blocking in aio applies on
  the next request. 404 for non-admins, local accounts and `AUTH_PROVIDER=local`;
  503 when aio can't be reached (fails closed), rest of tally unaffected.
- Called "Site admin" / "Admin situs" so it isn't confused with a tally's own
  admins. Overview: accounts, tallies run, last activity (audit log). User page:
  tallies with role, visibility, players, play days, matches, last played;
  private tallies listed but not linked.
- `sessions.is_admin_hint` (status at login) only shows the nav link.
  `admin_views` records every admin page view. Metric `tally.admin.checks{result}`.
- `AIO_INTERNAL_URL` (optional) sends the check to aio's cluster address;
  empty uses `AIO_ISSUER`.
- `sub` is parsed as a UUID before it goes into the URL; the two gosec G704
  lines carry `#nosec` with the reason (host is config, path is a parsed UUID).
- Tested end to end against a local aio + Postgres (curl-scripted aio login):
  link only for the admin, 404 for others, live demotion, 503 with aio down,
  audit rows; screenshots taken with Playwright.

## P9 notes: password reset links (2026-10-27)

- all-in-one RFC-003 (password reset by email) keeps every password form on
  aio; apps link to its pages. "Lupa kata sandi?" on the landing page opens
  aio's `/forgot-password`, and the new `/akun` page ("Akun" in the nav, aio
  login only) links to aio's `/account` ("Ubah kata sandi & email").
- Links carry `client_id=tally`, `return_to` (tally's origin from
  `AIO_REDIRECT_URL` + `/login` or `/akun`; aio drops it unless it matches a
  registered URI) and `ui_locales` from the current language.
- Before leaving, tally shows "you'll be taken to <aio host>, where your
  account is managed"; the host comes from `AIO_ISSUER` (RFC-003 §6.8).
- Guide: step 1 names aio's address and asks for an email; two FAQ entries
  (forgot password, add email / change password), aio login only. The guide
  now reads `pageData.Aio` instead of its own flag.
- Needs aio with RFC-003 deployed; until then the links lead to aio pages that
  don't exist yet, so merge after all-in-one#36 is live.

## RFC-001 S1 notes: points per player (2026-10-30)

- Play day gets two tabs, **Poin** (default) and **Pertandingan**, for
  non-counter tallies whose season is a race to a target (`continuous`, e.g.
  badminton to 42). Other formats keep the single page. The last tab is
  remembered in a `round_tab` cookie; `?tab=` switches.
- Poin tab: "Input poin" (player `<input list>` + points 0..target+win bonus,
  one entry = one play), "Poin hari ini" (a row per player: score chips in
  entry order, then grey chips from matches that link to the match, `+`, day
  total), and the extras ("Poin tambahan", collapsed) moved here with the same
  one-player form (name, ±points, optional note; unknown names refused) instead
  of a box per player. Counter tallies and other formats keep the grid.
  Attendance & courts stay on Pertandingan.
- `point_awards.kind`: `match` (has `match_id`, enforced by a CHECK), `score`
  (Poin tab, counts as a match played on the board, player page and tie-break),
  `extra` (bonus/penalty/counter). Upgrade backfills `match` for rows with a
  match_id; checked on a copy of the dev DB.
- Cap of 7 plays per player per day counts scores plus matches (scheduled
  too); it is enforced on Poin entries only, so matches still work as before.
  A score also ticks the player as present.
- Unknown name: the form asks before adding the player (`confirm_new=1`).
- Changes from RFC §7.1: no `seq` column (entries are ordered by time; the
  import will write them in slot order); kind has three values, not two.
- Play days are named by week of the month like the sheet ("Minggu 4",
  "Minggu 4A"/"4B" when a season has several play days that week; days 1–7 =
  1 … 29–31 = 5), no month. `Round.Number` (sequence) is still used for order.
- Mulai hari main asks only for the date and shows which season it falls in
  (or a warning, with a link to Musim). Attendance and courts left the form:
  scores tick players present, courts carry over from the season's last play
  day, and both stay editable on the Pertandingan tab. The old start-day
  guide screenshots were removed.
- Leaderboard shows the whole season (movement since before the latest play
  day); the "Minggu" picker filters to one play day's points only (no
  movement column). Poster follows the same filter.
- Main tab always shows the start form (no jump to today's play day); today's
  play day, if any, is a card on top. The play day's "Hari lain" became
  "+ Hari main baru". Tab order: Ringkasan, Main, Musim, Pemain, Setelan.
- Musim: a season's name opens `/periods/{id}` with totals and every week of
  the month in the season (Minggu 1–5 per month, empty ones too), each play
  day with players, plays, points, top player, and a link to its board.
  Editing moved to `/periods/{id}/edit`.
- Seasons end by their dates only (no close button; end early by editing
  the end date). Status badge Akan datang / Berjalan / Selesai on the Musim
  list, the season page and the board. When no season runs, the Main tab
  says the last one ended (with "Buat musim baru") or when the next starts.
- Board: a points-only play day shows "entered without match details" where
  the match results would be. Metric `tally.scores.recorded`.

## RFC-001 S3 notes: sheet import (2026-10-30)

- Season page → "Impor dari spreadsheet" (race-to-target seasons only):
  upload .xlsx (2 MB, first sheet) → preview → confirm. The preview carries
  the parsed sheet in a hidden field; confirm re-validates it, so nothing is
  stored between the steps.
- Parser (`xlsx.go`, `sheetimport.go`, stdlib only): finds each play day by
  its SubTot header, slots 1..n (≤ 7) before it, date in the row above
  ("Sun, 4 Oct", ISO, Excel serial; year from a cell above, else the season;
  Dec → Jan rolls over). `-`, blanks, -1 skipped; above the target capped.
  Notes: capped, -1 ignored, SubTot ≠ imported, sheet Total ≠ previous Total
  + SubTot, empty blocks, names that never played (not created). Errors
  (non-score cell, duplicate name/date, unreadable date) block the import.
  Zip parts are read with an 8 MB cap; 2000 × 400 cells max.
- Write (`ImportScores`): one transaction, creates players and play days,
  scores in slot order (created_at a millisecond apart), attendance, one
  `import`/`sheet` audit row. Days with points are skipped unless Replace
  (deletes that day's `score` entries only). Dates outside the season need
  the "extend season" tick.
- Checked with the club's October sheet: 5 play days, 392 scores, 14,778
  points; the board matches the sheet, except one running total the sheet got
  10 short and the -1 rows (now counted by the rule); re-upload
  skips all five days.

## Site admin: read-only tally pages (2026-10-30)

- opan: site admins (aio admins) may open every tally read-only, private ones
  included, because every view is logged. This widens P8 / RFC-002, which
  listed private tallies without a link.
- `/admin/users/{id}` links each tally to `/admin/tallies/{id}`: tally admins,
  seasons (status, play days, players, plays, points), players (plays,
  points over all seasons, last play day). From there:
  `/seasons/{pid}` (the Musim page with `Admin` set: no edit/import/board,
  play days link to the admin day page), `/players/{pid}` (the player page
  with admin links), `/days/{rid}` (scores per player, matches, extras,
  who was there; no forms).
- `admin_views` gained `tally_id` and `detail`; tally pages log the owner as
  `subject_user_id`. The admin tallies table counts plays instead of
  matches, so points-only tallies don't read 0.
- Tested with a fake aio status endpoint (`admin_test.go`): 200 for the
  admin on a private tally, 404 for others, four views logged, no tally form
  actions on any page.

## Open questions

- App name and domain (working name "Tally"; e.g. `tally.opan.dev`).

## Security backlog (carry over from cashflow)

Have from day one: bcrypt, HttpOnly/SameSite=Lax sessions, strict CSP,
body limits, parameterized SQL, html/template escaping, session pruning.
Done: double-submit protection (see above). Still to do: login rate limiting, CSRF tokens, least‑privilege DB role.
