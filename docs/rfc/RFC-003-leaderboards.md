# RFC-003: Leaderboards per sport inside a tally

- **Status:** Built; decisions in §3 (opan, 2026-11-01); plan in §7; as built in §12
- **Updated:** 2026-11-02
- **Author:** opan
- **Created:** 2026-11-01
- **Scope:** every tally; both login modes
- **Related:** NOTES.md "Phases"; RFC-002 (linked players are tally-wide); backlog: match
  formats per match (singles, doubles, mixed), sparring against another team

## Complexity score: **7 / 10**

Not much is new in itself: one table and a switcher. But almost every page that picks a season
today picks it "for the tally", and each of those has to pick it for a leaderboard instead.
Phase 1 (§7.1) does that with no visible change, which keeps the risk down.

## 1. Problem

A tally has one type, picked when it's created: badminton doubles, singles, team, counter… The
type decides how many players are on a side, which score formats a season may use, and the
poster's emblem. It can't be changed later.

A club rarely does just one thing. A badminton club may add a singles ladder next to its
doubles, or start playing basketball. Today that means a second tally, with its own players,
admins, link and branding, so everyone is entered twice and the club has two addresses.

## 2. Goals and non-goals

**Goals**

- A tally is the **club**: name, link, admins, players, visibility, branding.
- Inside it, one or more **leaderboards** (*papan*), one per sport or game, each with its own
  type, seasons, play days and board.
- A club with one leaderboard looks and works exactly as today.
- Existing tallies move over by themselves: each gets one leaderboard with its current type.

**Non-goals (for now)**

- A ranking that adds up several leaderboards (§3.4).
- Admins or visibility per leaderboard (§3.2, §3.3).
- Changing a leaderboard's type after it's created.
- Singles, doubles and mixed within one badminton leaderboard, and sparring against another
  team: separate RFCs. With this RFC a club can already add "Badminton tunggal" as its own
  leaderboard.

## 3. Decisions

Answered by opan on 2026-11-01.

1. **Naming:** "Tally" stays the word for the club, for now. Each sport is a **papan**
   (Indonesian) / **leaderboard** (English).
2. **Admins:** club-wide. A tally admin runs every leaderboard of that tally.
3. **Visibility:** per club. Public, link-only or private applies to all its leaderboards.
4. **Across sports:** leaderboards stay separate. No combined ranking.

## 4. Model

| Lives on the **tally** (club) | Lives on a **leaderboard** (sport) |
|---|---|
| Name, slug and link `/t/{slug}` | Name ("Badminton ganda", "Basket") and its own slug |
| Admins and owner | Type: one of today's presets, plus team size for team presets |
| Players (one member list) and their linked accounts (RFC-002) | Seasons, with their scoring rules and archive state |
| Visibility | Play days, matches, Poin entries, extras |
| Branding: logo and poster texts | The board, the poster (emblem from its type) |
| Audit log | |

Players are shared: someone who plays badminton and basketball is one player, linked to their
account once. Each board lists only the players with points in that leaderboard's season, as
it does today for a season.

## 5. Data and migration

### 5.1 Schema

```sql
CREATE TABLE leaderboards (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tally_id   uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    slug       text        NOT NULL,              -- unique in the tally, used in ?board=
    name       text        NOT NULL DEFAULT '',   -- '' = the type's translated name
    template   text        NOT NULL,              -- a preset id, as tallies.template today
    side_size  smallint    CHECK (side_size BETWEEN 1 AND 11),
    position   smallint    NOT NULL DEFAULT 0,    -- order in the switcher; lowest = default board
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tally_id, slug),
    UNIQUE (id, tally_id)                          -- target of the composite key below
);

ALTER TABLE periods ADD COLUMN leaderboard_id uuid;
-- a season's leaderboard must belong to the same tally
ALTER TABLE periods ADD FOREIGN KEY (leaderboard_id, tally_id)
    REFERENCES leaderboards (id, tally_id) ON DELETE CASCADE;
CREATE INDEX ON periods (leaderboard_id, starts_on DESC);
```

Seasons **keep `tally_id`**. That's the main simplification: every query that checks a play
day, match, score or season belongs to the tally in the URL joins `periods.tally_id` today,
and keeps doing so unchanged. Only the queries that choose *which* seasons to show
(`ListPeriods`, `CurrentPeriod`, `PeriodForDate`, `RoundByDate`) gain a leaderboard.

### 5.2 Migration (idempotent, at startup like the rest of `schema.sql`)

1. For every tally without a leaderboard, insert one with the tally's `template` and
   `side_size`, `name = ''`, `slug` from the type (e.g. `badminton`), `position = 0`.
2. Set `periods.leaderboard_id` to that leaderboard where it's NULL.
3. `ALTER COLUMN leaderboard_id SET NOT NULL`.

`tallies.template` and `tallies.side_size` are left in place, unused, for one release, then
dropped.

**Rollback:** an image from before this RFC still runs, but creating a season fails on the
NOT NULL column. Everything else, entering points included, keeps working. Rolling back is
therefore possible for a short while, but not something to stay on.

## 6. Pages and URLs

### 6.1 Which leaderboard a page shows

- Pages reached through a season, play day, match or player season (`/periods/{id}`,
  `/rounds/{rid}`, `/matches/{mid}`, `?season=`) already know their leaderboard from the
  season.
- Pages that pick "the current season" take **`?board={slug}`**: the board `/t/{slug}`,
  the Main tab `/play`, the Seasons tab `/periods`, the poster and the player page.
  Without it they use the **default leaderboard**: the one with the lowest `position`
  (oldest first on a tie), i.e. the top of the list in Setelan → Papan. Every link shared
  today has no `?board=`, so it keeps working and shows the default. The home page's tally
  card and the tally name in the header lead there too.
- Once on a leaderboard, the tabs and the switcher carry its `?board=`, so Main, Musim and
  Poster stay on it: an admin picks the leaderboard once per visit, not once per entry.
- Forms that create something (a season, a play day) carry the board in a hidden field.

### 6.2 What changes on screen

- **Switcher:** with two or more leaderboards, a row of buttons under the tally's name
  ("Badminton · Basket") on the board, Main and Seasons tabs. With one, nothing is shown.
- **Header:** the eyebrow ("Bulu tangkis · Ganda") shows the current leaderboard's type.
- **Setelan → Papan:** list of leaderboards with rename and order; **Tambah papan** (name +
  the type picker that's on the create-tally form today); delete only while it has no
  seasons.
- **Creating a tally:** unchanged. The type picked there becomes its first leaderboard.
- **Player page:** the season picker groups seasons by leaderboard; stats are per season as
  today.
- **Home page:** tally cards show the leaderboards' names when there are several. **Mabar
  saya** shows one line per leaderboard where the player has points in its current season.
- **Poster:** per leaderboard (it already follows the season); emblem from its type.
- **Site admin pages:** the tally page groups seasons by leaderboard.
- **Unchanged:** players, admins, owner transfer, invites, visibility, branding, archive (per
  season), the sheet import (per season).

## 7. Implementation plan

Two code PRs, one after the other, each from `main`, plus the docs in the second.

### 7.1 Phase 1: one leaderboard behind the scenes (no visible change)

Goal: the data model and code paths move to leaderboards while every tally still has exactly
one, so the app looks and behaves the same and can be deployed on its own.

1. **Schema:** §5.1 and the migration in §5.2.
2. **Store:** `Leaderboard` type with `Preset()` and `SideSize()` (moved from `Tally`);
   `ListLeaderboards(tallyID)` (by `position`, then `created_at`), `LeaderboardBySlug`,
   `DefaultLeaderboard` (the first of that list);
   `ListPeriods`, `CurrentPeriod`, `PeriodForDate`, `RoundByDate` take a leaderboard id;
   `CreatePeriod` sets it; `CreateTally` also creates the first leaderboard in the same
   transaction. `Period` gains `LeaderboardID`.
3. **Request context:** `loadTally` loads the leaderboards and puts the chosen one in
   `tallyCtx.Board` (from `?board=`, else the default). Pages that load a season or play day
   set `Board` from it instead.
4. **Callers:** every use of `tc.Tally.Preset()` / `SideSize()` / `Template` moves to the
   leaderboard: board and player pages (counter), match form and shuffle (side size), season
   form (allowed formats), `scoreLimit`, poster emblem, `tallyhead`, `MyPlayers`, metrics.
5. **Tests:** migration on a database with existing tallies, seasons and play days (one
   leaderboard each, every season assigned); all existing tests pass unchanged; a test that a
   season can't point at another tally's leaderboard; every tally page renders.

Size: medium. Touches the store, `handlers_*`, a few templates; no new pages.

### 7.2 Phase 2: several leaderboards

1. **Setelan → Papan:** add, rename, reorder, delete-if-empty (`POST /t/{slug}/boards`,
   `POST /t/{slug}/boards/{id}`), audit entries, owner and admins both allowed (§3.2).
2. **Switcher** partial and `?board=` on board, Main, Seasons, poster and player links;
   hidden fields on the create-season and start-play-day forms.
3. **Season defaults** per leaderboard: "Musim #n" numbering and the copied rule come from
   that leaderboard's latest season.
4. **Home page:** tally cards and Mabar saya as in §6.2.
5. **Site admin:** group seasons by leaderboard.
6. **Metrics:** `tally_leaderboards` gauge; `tally_leaderboards_created_total{type}`.
   `tally_tallies_created_total{type}` keeps the first leaderboard's type.
7. **Docs:** guide §7 "Beberapa papan dalam satu tally" (ID + EN) with screenshots
   (settings card, switcher), README metrics, NOTES phase row and notes, this RFC's
   "As built".
8. **Tests:** two leaderboards with seasons over the same dates and play days on the same
   date; each board, Main tab and poster shows only its own; the default board without
   `?board=`; an unknown `?board=` gives 404; delete refused once a season exists; private
   tally still private on every board; Mabar saya lines per leaderboard.

Size: large, mostly UI.

### 7.3 Order and tracking

Phase 1 PR → merge and deploy → phase 2 PR. One Deck card per phase.

## 8. Risks

- **A missed caller** still asking for "the tally's" season would show the default
  leaderboard on another leaderboard's page. Phase 1 removes `Tally.Preset()` and the
  tally-only season lookups, so the compiler finds every Go caller; templates call them too,
  so phase 1 also renders every tally page in a test.
- **Same date on two leaderboards:** today a tally has at most one play day per date
  (`RoundByDate`). After this, one per leaderboard per date; the Main tab's "today" button
  is per leaderboard.
- **Links:** `/t/{slug}` and every link shared today keep showing the default leaderboard.

## 9. Small questions, with proposed answers

1. **URL form:** `?board=basket` (proposed) rather than `/t/{slug}/basket/...`, which would
   double every route and clash with names like `players`.
2. **Deleting a leaderboard:** only while it has no seasons (proposed). Archiving seasons
   covers "we stopped playing this".
3. **Default leaderboard:** the first one; reorder in Setelan to change it. **Agreed** (opan,
   2026-11-02). A new leaderboard goes to the bottom, so adding one never changes what
   `/t/{slug}` shows; moving another to the top does, while links with `?board=` keep their
   leaderboard. The default only decides where a link without `?board=` lands; it has no
   effect on points, rankings, admins or visibility. Considered and dropped: a separate
   "Jadikan utama" control (one more setting for something rarely changed), and "the one
   played most recently" (links would jump between sports).

## 10. Backlog (after this)

- Singles, doubles and mixed within one badminton leaderboard (format per match).
- Sparring against another team.
- A combined ranking across leaderboards, if a club asks for one (§3.4).
- Admins per leaderboard, if a club asks for one (§3.2).

## 11. Next steps

- [x] opan decides §3 (2026-11-01).
- [x] opan reviews this plan, including §9 (2026-11-02; §9.3 default = lowest position).
- [x] Phase 1 (§7.1): PR #21, deployed 2026-11-02.
- [x] Phase 2 (§7.2): this PR.

## 12. As built

### 12.1 Phase 1 (PR #21)

As planned, with these details:

- No `LeaderboardBySlug`: `loadTally` loads all of a tally's leaderboards (a handful) and picks
  from them. `tallyCtx.Boards` holds them, the default first.
- `?season=` on the board and player pages also picks the season's leaderboard (`seasonBoard`),
  so a season link works without `?board=`.
- `tallyVM.Board` (the standings) became `Standings`, since `Board` is now the leaderboard.
- The upgrade names the first leaderboard's slug after the type: `badminton` for both badminton
  presets, else the preset id with `-` (`team`, `counter`…).

### 12.2 Phase 2

- **Links:** board-wide pages carry `?board=` only when the tally has **two or more**
  leaderboards (`tallyCtx.BoardPath`), so a one-leaderboard club keeps exactly today's URLs.
  Forms that create a season or a play day post to an action with `?board=` (no hidden field),
  and redirects keep it. Pages tied to a season, play day or match need nothing: they know theirs.
- **Switcher:** pills under the tally's name on Ringkasan, Main and Musim (also on a play day or
  season page, whose tab is Main or Musim); a pill keeps the current tab. Shown to viewers too.
  The type line above the name shows only for a named leaderboard there (the pill already says
  the type otherwise), and not on Pemain or Setelan.
- **Setelan → Papan:** list with the default marked *Utama*, its type, season count and
  `?board=` slug; *Ubah* opens rename, up/down and delete. Delete only while it has no seasons
  and never the last one. The add form reuses the create-tally type picker. Slugs come from the
  name (else the type), made unique with `-2`, `-3`…, and never change on rename. At most 10 per
  tally. Every change is audited (`leaderboard` create/update/delete).
- **Home page:** a tally card lists its leaderboards' names when there are several. *Mabar saya*
  has one line per leaderboard where the player has points in its current season, else one for
  the default; the line names the leaderboard and links to the player page on that season.
- **Player page:** the season picker groups seasons by leaderboard (`<optgroup>`).
- **Site admin:** the tally page lists the leaderboards with their types, and groups the seasons
  table under each leaderboard's name.
- **Metrics:** `tally_leaderboards_created_total{type}` counts leaderboards added after creating
  a tally (a new tally's first one stays in `tally_tallies_created_total`). The
  `tally_leaderboards` gauge comes once the metrics PR (#23) is in, as it shares its code.
- **Guide:** §7 "Beberapa papan dalam satu tally" / "Several leaderboards in one tally", with two
  screenshots (`boards`, `switcher`); §2 points to it for another sport or type.
