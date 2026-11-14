# RFC-005: Club menus (implementation plan)

- **Status:** Draft; plan for opan to review. The questions in §5 block only their own step.
- **Updated:** 2026-11-14
- **Author:** opan
- **Created:** 2026-11-14
- **Scope:** every tally; both login modes
- **Related:** RFC-003 (leaderboards and seasons), RFC-004 (Iuran, PR #28); Deck 342 (menu
  instead of tabs), Deck 327 (best of 3), Deck 358 (payments)

## Complexity score: **roadmap**, each step scored on its own

This is the order of work, not one feature. Every step below gets its own short RFC (or a section
here, if it's small) and its own PRs to `main`. Sizes use the RFCs' 1–10 score: **S** 1–3,
**M** 4–6, **L** 7–8, **XL** 9–10.

## 1. Problem

Early users of the club tally asked for five menus:

1. **Mabar**: the weekly play night.
2. **Sparing**: a friendly against another club.
3. **Turnamen**: internal, external, and as organizer.
4. **Events**: for now only the club's points league, which is what tally already does.
5. **Database**: the member list.

Today tally has tabs for one leaderboard at a time (Ringkasan, Main, Musim, Pemain, Setelan).
Tally already has some of what they asked for, but under other names. The rest is new.

## 2. Where each menu stands

| Menu | In tally now | New | Size |
|---|---|---|---|
| Mabar | Play days, Poin, matches and shuffle, attendance | Iuran (RFC-004); a recap of the night; a regular schedule | Iuran M, recap S, schedule M |
| Events | Leaderboards (papan) with seasons, ranking, poster, archive | A list with status and leader; rules and prize text; papan renamed Event | S |
| Database | Players: display name, active, linked account | Level, gender, member or guest, contact (admins only), search, Excel import | M |
| Turnamen eksternal | Extra points with a reason | A record of tournaments members played elsewhere, results on player pages, optional bonus points | S |
| Sparing | Nothing | Club vs club: partai, game scores, result, history, player record | M |
| Turnamen internal | Nothing | Categories, entries, draw, bracket or groups, champion | L |
| Turnamen organizer | Nothing | Hosting for other clubs: sign-up, fees, schedule, public live page | XL |

## 3. Rules for every step

- **Nothing new until it's used.** A tally that never opens a new menu looks as it does today,
  the same rule RFC-004 uses for the fee.
- **History is kept.** Records get an undo or an edit, both audited in `audit_log`. Nothing a
  member can see disappears quietly.
- **Each step ships whole:** schema in `schema.sql` (idempotent), store, pages, `locales/id.json`
  and `en.json`, tests, a guide section with screenshots, metrics in README, and a Grafana panel
  in homelab.
- **Names in the repo stay made up** (Badminton Sampai Pingsan, Jacque Oui, Prabski…).
- **Points are only changed by an admin's explicit step.** Sparing and tournament results never
  change the points league by themselves. When a club wants bonus points, the admin taps "add as
  extra points", which writes an ordinary `point_awards` row (`kind = 'extra'`), linked back so
  it's added once only.

## 4. Steps

The order differs a little from the sketch page: the menu comes third instead of fourth. Each new
feature would otherwise add a tab to a row that is already full on a phone, and Iuran adds the
sixth.

### Step 1. Mabar · Iuran (M), RFC-004

As in RFC-004: a fee per play day, payments recorded one by one (Tunai, Transfer, Gratis), the
history for everyone. Phase 2 links to cashflow.

- **Blocks:** RFC-004 §9.3 (where the history lives), §9.4 (undo), §9.6 (cashflow).
- **PRs:** RFC-004 (#28); phase 1; phase 2 is optional and goes cashflow first, then tally.
- **Then:** the member and guest price (needs step 2) is the likely next ask.

### Step 2. Database (M), RFC-006

The member list, which the later steps rely on. Sparing line-ups pick from it, tournament
categories need gender, and draws balance by level.

```
participants
  + gender      text NOT NULL DEFAULT ''  CHECK (gender IN ('', 'm', 'f'))
  + level       text NOT NULL DEFAULT ''  -- the tally's own scale, e.g. A+, A, B+, B, C
  + membership  text NOT NULL DEFAULT ''  CHECK (membership IN ('', 'member', 'guest'))
  + phone       text NOT NULL DEFAULT ''  -- admins only
  + joined_on   date
  + note        text NOT NULL DEFAULT ''  -- admins only
tallies.branding (or settings) + levels: the ordered list of level names
```

- **Pemain becomes Database:** search by name, filter by level, gender, and member or guest.
  It has an edit form per player and a bulk edit for level.
- **Privacy:**
  - Phone and note show only to the tally's admins, never on public pages, the poster or the
    site admin pages.
  - Level shows to everyone only if the tally turns that on.
- **Excel import** of a member list, reusing `xlsx.go` and the preview-then-confirm flow of
  the points import. It matches existing players by name and adds the rest.
- **Shuffle** can balance pairs by level (optional, a later PR in this step).
- **Metrics:** `tally_players{membership}`. Members are counted, never their personal data.
- **PRs:** fields and the edit form; search, filters and import; balance by level.

### Step 3. Menu and Events (S, menu work M), Deck 342

The five menus get a home, and the tab row goes away.

- **Menu:**
  - On a phone, a bottom bar with Mabar, Events, Turnamen and Database, and "Lainnya" for
    Sparing and Setelan.
  - On a wider screen, a left sidebar.
  - A menu shows only once it has something in it, or the admin turns it on in Setelan.
- **Mabar** is today's Main and play day pages, plus Iuran.
- **Events** is today's leaderboards, as a list:
  - Status: Berjalan (current season), Segera (season starts later), Selesai (archived).
  - The current leader on each.
  - New text fields on the leaderboard for rules and prize.
  - Ringkasan and Musim open from an event.
- **Rename:** "papan" becomes "Event" in the UI only (if opan agrees, §5). Routes and
  `?board=` stay as they are, so shared links keep working.
- **PRs:** the menu shell with today's pages behind it, with no new features; then the Events
  list.

### Step 4. Turnamen eksternal (S), RFC-007

A record of tournaments members played at other clubs.

```
tournaments(id, tally_id, kind CHECK (kind IN ('external', 'internal')),
            name, organizer, venue, starts_on, ends_on, note, created_by, created_at)
tournament_entries(id, tournament_id, category text,          -- MD, WD, XD, MS, WS, open
                   result CHECK (result IN ('champion', 'final', 'semi', 'quarter',
                                            'group', 'played')),
                   award_id → point_awards NULL,              -- set once bonus points are added
                   created_at)
tournament_entry_players(entry_id, participant_id, PK (entry_id, participant_id))
```

- **Admins** add a tournament, then entries: the category, our player or pair, and how far
  they got.
- **Everyone** sees the Turnamen list (an achievements wall) and "Turnamen: juara 1× · semifinal
  2×" on player pages.
- **Bonus points:** "Tambah poin ekstra", with points per result, the season, and the
  tournament's date. It writes one extra per player and sets `award_id`.
- **PRs:** one, with the tables shaped so step 6 can reuse them.

### Step 5. Sparing (M), RFC-008

A friendly against another club, decided partai by partai.

```
sparings(id, tally_id, opponent, played_on, venue, note,
         format CHECK (format IN ('bo3_21', 'single_21', 'single_42')),
         created_by, created_at)
sparing_partai(id, sparing_id, position, category, their_names text, created_at)
sparing_partai_players(partai_id, participant_id)
sparing_games(partai_id, n, ours, theirs, PRIMARY KEY (partai_id, n))
```

- **Admins:**
  - Create a sparing (opponent, date, number of partai).
  - Fill in each partai: category, our pair from the Database, their names as plain text,
    and game scores.
- **Results are computed, never typed:**
  - A partai's winner comes from its games (two games of three for `bo3_21`).
  - The sparing's result counts partai won, e.g. 3–2.
- **Everyone** sees the history, a page per sparing, and "Sparing: 5 menang · 2 kalah" on
  player pages.
- **Bonus points:** optional, the same "add as extra points" as step 4.
- **Shared with Deck 327 (best of 3):** game-score checks (21, deuce to 30) live in
  `scoring.go`, so matches can use them later.
- **PRs:** one, maybe two (record, then player stats).

### Step 6. Turnamen internal (L), RFC-009

The club runs its own tournament among members.

- **6a · Setup:**
  - A tournament with `kind = 'internal'`, categories, and entries.
  - Entries are pairs picked by the admin, or drawn by level with step 2 data and the
    shuffle code.
- **6b · Knockout:**
  - A bracket for 4 to 32 entries, with byes and seeds.
  - Scores use step 5's game rules. The winner moves on by itself, and an admin can correct a
    result.
  - A public bracket page.
- **6c · Groups:** round robin in groups, standings, then the top two into the knockout.
- **Results** fill `tournament_entries.result`, so the achievements wall and bonus points from
  step 4 work unchanged.
- **PRs:** one per sub-step.

### Step 7. Mabar · recap and schedule (recap S, schedule M)

These can go in anywhere after step 1, whenever the club asks.

- **Recap:** one page per play day after the night: who came, the day's top points, the money
  (from Iuran), shared as a link or an image like the poster.
- **Schedule:**
  - A regular slot on the leaderboard: day, time, venue, courts.
  - Main then shows "Mulai mabar Minggu 16 Nov" prefilled, one tap.
  - Tally has no background jobs, so nothing is created ahead of time.

### Step 8. Turnamen organizer (XL), not planned yet

Hosting for other clubs needs:

- people with no tally account to sign up;
- entry fees (Iuran grown up);
- a court schedule across many courts;
- a live public page.

It's close to a separate product. This RFC stops at naming it. Plan it when the club has a real
tournament to host, by building on step 6.

## 5. Questions for opan

Each question only blocks its own step.

1. **Step 1:** RFC-004 §9.3, §9.4, §9.6.
2. **Step 2:**
   - Which member fields does the club really need? Fewer is safer, since each is personal
     data.
   - What is the club's level scale?
   - Should levels be public?
3. **Step 3:** Is "Event" the word members use for the points league? Then papan becomes Event in
   the UI.
4. **Steps 4 and 5:** Do tournament and sparing results earn bonus points? If so, how many per
   result?
5. **Step 5:** What format does a sparing use: best of 3 to 21, or one game to 21 or 42?
6. **Step 6:**
   - Does the club run internal tournaments now?
   - Knockout only, or groups first?
7. **Step 7:** Track shuttlecocks (provided, used, left)? Only worth it if someone counts them
   today.

## 6. Risks

- **Menu change (step 3)** moves every page members know. Mitigation: the first PR only moves
  today's pages, with nothing new, and the guide gets new screenshots in the same PR.
- **Personal data (step 2).** Tally starts holding phone numbers. Mitigation: admins only,
  covered by tests on every public page and the poster, and the fields are optional.
- **Scope creep in Turnamen.** Three features under one word. Mitigation: built as separate
  steps, organizer explicitly not planned.
- **Points league fairness.** Bonus points from outside play can upset members. Mitigation:
  never automatic, always an admin's step that's visible as an extra with a reason.

## 7. Next steps

- [ ] opan reviews this plan and the order.
- [ ] Step 1: RFC-004 answers, then the phase 1 PR.
- [ ] Each later step starts with its RFC and its Deck card.
