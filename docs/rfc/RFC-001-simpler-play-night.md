# RFC-001: A simpler play night

- **Status:** Draft; first slice decided (§6), design proposed (§7)
- **Author:** opan
- **Created:** 2026-10-29 · **Updated:** 2026-10-29
- **Scope:** badminton first; other tally types keep working but are not designed for here
- **Related:** NOTES.md (P2 play-day notes), the in-app guide `/panduan#hari-main`

## Complexity score: **5 / 10** (first slice, §7)

Points entered per player, a spreadsheet-style view, and an import of the club's sheet.
Small schema change (entry kind and order on `point_awards`, a play-day label); the XLSX
import is the largest part. The live-night items in §5.1 come later and would score higher.

## 1. Problem

Early feedback from club members using tally: **the UX still feels too complex.**
We want people to use tally *during* the play night and keep coming back to the board,
not to treat it as a chore one admin does afterwards.

What we don't know yet (to be gathered, see §10):

- **Who** finds it complex: the admin running the night, or the players opening the link?
- **Where**: first-time setup, the play-day screen, entering scores, or reading the board?
- **When** they give up: before the first match, mid-night, or after?

### 1.1 Today's flow, for reference

First-time setup (the guide's "Quick start"): sign up → create a tally and pick one of
six types → type in player names → create a season and pick a points rule → start playing.

A play night, on one page (`/t/{slug}/rounds/{id}`):

1. **Play** tab → pick the date, number of courts (1–3) and tick who is present → *Start*.
2. **On court** card → *Shuffle* fills the free courts.
3. Each court has a collapsed score form → enter the score → *Save*. Or cancel the match.
4. **Finished** card lists scored matches; *Add a match manually* is a second, separate form.
5. **Extra points** (bonus/penalty) is a third collapsible card.
6. **Attendance & courts** sits at the bottom as a fourth collapsible card,
   which is where late arrivals are added.

Places this flow may be heavier than it needs to be (hypotheses until §7 confirms them):

- **Setup before the first match:** five steps, including two the admin may not understand
  yet (tally type, points rule). Players and a season must exist before you can play.
- **Four cards on one page**, three of them collapsible, each with its own Save button.
  Two different score forms (on court vs manual).
- **Late arrivals** mean scrolling to the bottom card, ticking, and saving. **Early leavers** can't be
  marked at all: un-ticking is the only way to stop them being shuffled in.
- **Courts are numbers 1–3**, not the hall's names, and clubs with more than three courts don't fit.
- **No "next up":** while all courts are busy, nobody can see who plays next.
- **Players** who open the link see a leaderboard, but nothing about *tonight* (who's on court,
  who's next, their own matches).

## 2. Goals

- An admin can run a whole night from one screen, one-handed, on a phone, between rallies.
- A brand-new admin gets to their first recorded match in **under two minutes**.
- Players who open the link see something useful *tonight*, so they open it again.
- Keep what already works: fair automatic shuffle, the points/ranking engine, the poster.

## 3. Non-goals (for now)

- **Money:** session fees, memberships, outstanding payments, club cashflow.
  This overlaps the separate cashflow app (§8, open question 1).
- Non-badminton tally types get no new features in this RFC.
- Native mobile apps.

## 4. References

### 4.1 BadminHub

A user shared the guide of **BadminHub** ("Play together, manage smarter"), an app
for running a badminton club's regular *mabar* nights. It is a useful reference
because it is built around the live night, which is exactly where tally feels complex.

BadminHub is about **running the night and the money**; tally is about **points and
ranking**. They overlap only in the live session.

| BadminHub | Tally today | Fit for tally |
|---|---|---|
| **Match lifecycle**: Draft → Playing → Finished, on a named court ("Court C – Atas"). Drafts are planned while courts are busy, then *Start*, *Recompose* or *Void*. | `scheduled` → `done`. Shuffle only fills free courts; no queue. | **High:** a "next up" queue makes changeovers instant. |
| **Courts**: named, up to 100 per event. | Numbers, **1–3 per play day** (DB check). | **High:** three is too few for bigger clubs, and halls name their courts. |
| **Check-in / checkout**: arrival time recorded; *Checkout* for early leavers. New match only offers players who are checked in, not checked out, and idle. | Present/absent tick; shuffle skips players on court. | **High:** checkout keeps leavers out of the shuffle; arrival time makes "waited longest" real. |
| **New player on the spot**: *Create & check in* from the session page. | Players are added on the Players tab. | **High, cheap.** |
| **Shuttlecocks**: provided per night, +/− used per match, recap shows provided / used / remaining. | None. | **Medium:** badminton-specific, small, clubs track it. |
| **Session recap**: attendance (members / others), matches, shuttles, cash. | Play-day results on the board. | **Medium:** the non-money parts fit. |
| **Player details**: alias, WhatsApp, gender, level (A+…C), age range. | Display name only. | **Medium:** *level* is the valuable one; the rest is personal data. |
| **Excel import** of players (6 columns, duplicate phone numbers skipped, 2 MB / 5000 rows). | None. | **Medium:** saves typing when a club moves to tally. |
| **Recurring events** (weekday, time, hall, courts, default fees), then each date scheduled as a "night". | Season + one play day per date; courts picked each time. | **Low–medium:** a weekly template that pre-fills the play day. |
| **Halls/venues** with address. | None. | **Low:** only matters for clubs in several halls. |
| **Memberships, fees, outstanding payments, ledger.** | None. | **Out of scope** (§3). |
| *Missing in BadminHub:* automatic pairing, points, ranking, seasons, poster. | Tally has all of these. | Tally's own strengths. |

Details worth copying:

- **The score is optional.** *Finish match* is separate from entering a score.
- **End session** closes the night: every match must be finished first, and it can't be reopened.
- **Corrections** history shown on each finished match (tally's audit log already has the data).
- **Search** for big player lists (their example club has 1121 players).
- One screen with three tabs (*Players · New match · Matches*) instead of stacked cards.

What BadminHub does *not* do: it has **no automatic pairing**. Admins choose all four players
by hand and queue drafts. Tally's shuffle (fewest matches tonight → waited longest)
is already ahead; its natural next step is **level-balanced pairing**, using A+…C so
the shuffle avoids lopsided matches. BadminHub collects levels but doesn't use them.


### 4.2 The club's spreadsheet ("BALA BULU POINT")

The club keeps its leaderboard in a Google Sheet today. This is what tally is compared
with, so it shows what "simple" means to these users. Analysed 2026-10-29 from an XLSX
export of the October sheet. Player names are left out here; the repo is public.

**Layout.** One sheet. A row per player (65 players), a column block per play day:

| Columns per play day | Meaning |
|---|---|
| `1` … `7` | **That player's own score** in each match they played that day (42 = won a race to 42). `-` = no match. |
| `SubTot` | Sum of the day's scores |
| `Total` | Running total for the season |
| `Rank` | Rank by total, **top 10 only** |
| `Movement` | `up` / `down` / `stay` against the previous play day (from the third play day on, top 10 only) |

Block headers carry the date and a week label: `Sun, 4 Oct · W1`, `Sun, 11 Oct · W2`,
`Sun, 18 Oct · W3`, `Fri, 23 Oct · W4A`, `Sun, 25 Oct · W4B`, then an empty `Sun, 2 Nov`.
The year (`2026`) and month (`OCT`) sit in column B above the blocks.

| Play day | Players | Matches (approx.) | Matches per player |
|---|---|---|---|
| W1 · Sun 4 Oct | 15 | 17 | 3–7 |
| W2 · Sun 11 Oct | 23 | 25 | 2–7 |
| W3 · Sun 18 Oct | 9 | 12 | 4–7 |
| W4A · Fri 23 Oct | 22 | 25 | 2–7 |
| W4B · Sun 25 Oct | 19 | 21 | 3–6 |

22 of the 65 listed players have not played on any of these days.

**No formulas.** The export has none: subtotals, totals and ranks are typed by hand, and
the sheet already has mistakes:

- One running total is **10 points short** (722 + 140 written as 852).
- A **`-1`** in the 7th slot of one W1 match for four players. Three of their subtotals include it, one doesn't.
  Meaning unknown: a penalty, or a "not played" mark?
- Two scores of **43** in one W1 match (typo, or play went past 42).
- W1 has 30 winning scores (42) against 38 others: some matches ended with nobody at 42
  (court time ran out). Tally already accepts that, and accepts 43.

**What it tells us about the UX:**

1. **Entering a score per match is not the hard part.** The sheet needs 4 cells per match;
   tally's shuffle knows the four players, so it needs 2 numbers. The weight is elsewhere:
   setup, collapsed cards, finding the right form.
2. **But the sheet doesn't need the pairs at all.** The admin only writes each player's
   score. That's the model users already know, and the reason for the first slice (§6).
3. **People read the whole grid:** every player sees their own row, every match score,
   week after week. Tally shows totals; detail is a tap away on the player page.
4. **The club counts weeks of the month, not play days.** Two play days in week 4 are
   `W4A` and `W4B`; tally's "Minggu #N" would call them #4 and #5.
5. **The hard-coded 7 slots** is a limit of the sheet, not a rule: several players hit 7.
6. **Top 10 with movement** is what they share, which matches tally's poster.

## 5. Candidate directions

Ideas from §1.1 and §4.1, **ranked below the first slice** (§6, §7). They stay here for later.

### 5.1 The live night (badminton-first)

1. **One night screen** with tabs (*Players · Courts · Matches*) in place of four stacked cards.
2. **Named courts** and raise the 1–3 limit.
3. **Next-up queue:** shuffle (or the admin) drafts upcoming matches while courts are busy;
   *Start* puts one on the free court.
4. **Check-in and checkout** per player with arrival time; *add & check in* a new player in place.
5. **One score form** for every match; score optional, *finish* separate.
6. **End night** to close the play day and show a recap.

### 5.2 Getting started

7. **Defaults instead of questions:** create a badminton-doubles tally with a default season
   and points rule in one step; change them later in Settings.
8. **Start playing first, add details later:** type names straight into the night's check-in,
   which creates the players.
9. **Player import** from CSV/XLSX for clubs moving in with a list (partly covered by §7.4).

### 5.3 For players (engagement)

10. **Tonight view** on the public link: who's on court, who's next, results so far.
11. **"My matches"**: a player's own results and rank movement after each night.
12. **Level-balanced pairing** so matches feel fair (needs a level per player).
13. **Shuttlecock count** and a night recap (attendance, matches, shuttles).

## 6. Decisions (opan, 2026-10-29)

1. **First priority: the admin enters points for every player.** One number per player per
   match, like the sheet. Recording the match itself (who partnered whom, who won) is
   **lower priority** and stays optional.
2. **Keep the sheet's format:** players × play days, a score per match, subtotal, running
   total, rank, movement.
3. **No fixed number of matches per player per day.** The sheet's 7 slots are its limit,
   not the club's rule.
4. **Backfill from the sheet, then switch.** The admin imports the existing sheet once; after
   that, points are entered in tally directly and the sheet is retired.
5. **Season dates stay editable** by the admin, e.g. from the first week of October to the
   fourth week of December.
6. **Picking a player** when entering points is a dropdown that also accepts typing, with
   autocomplete on the name.
7. **Backlog:** linking a player in the list to a real tally user (§9).

## 7. First slice: points sheet (proposed design)

### 7.1 Data

Points-only entries reuse `point_awards`, which already holds per-player points tied to a play
day (`round_id`) without a match (`match_id IS NULL`, today used for extra points).

- Add `kind text NOT NULL DEFAULT 'extra' CHECK (kind IN ('score','extra'))`:
  - `score`: a player's score in one match. **Counts as a match played** (for "played" and
    the average on the board).
  - `extra`: today's bonus/penalty. Changes the total, not "played".
  - Match-derived awards (`match_id` set) keep working and count as played, as now.
- Add `seq smallint` for the slot order within a player's play day (1, 2, 3, … no upper
  limit in the schema; a sanity cap in the form, see open question 6).
- Totals, rank and movement are **always computed**, never stored or typed. That removes the
  sheet's arithmetic mistakes.

### 7.2 Entering points (admin, on the play-day page)

- A **"Input poin"** card at the top of the play day:
  - **Player:** a text input bound to a `<datalist>` of the tally's players. It drops down on
    tap and narrows as you type. Native HTML, no script, so it fits the strict CSP and works on
    phones.
  - **Points:** a numeric input (`inputmode="numeric"`).
  - **Tambah** saves one entry and puts the cursor back in *Player* for the next one.
    It uses the existing double-submit guard.
  - A name that matches no player is not saved silently: the form asks "add new player
    *X*?" to avoid duplicates from typos.
- Below it, **today's rows**: each player who has entries, their scores as chips in order
  (`42 · 35 · 40`), the subtotal, and a **+**. Tap a chip to edit or delete it.
- Players with entries count as present, so the attendance list fills itself.
- Matches and the shuffle stay on the page for clubs that use them; for this club's tally the
  points card comes first.

### 7.3 The sheet view

A grid page per season, like the sheet: players as rows (name column sticky), play days as
column groups with each score, then subtotal, running total, rank and movement. It scrolls
sideways on a phone. An admin sees **edit** on each cell group, which opens that play day's
entries for the player.

Week labels follow the club: **the week of the month**, from the date (days 1–7 → W1,
8–14 → W2, 15–21 → W3, 22–28 → W4, 29–31 → W5), with `A`/`B` when a week has more than one
play day. That reproduces `W1 … W4A, W4B` exactly. Shown in the grid, on the board's play-day
picker and on the poster.

### 7.4 Importing the sheet

- **Upload an `.xlsx`** (Google Sheets: *File → Download → Microsoft Excel*). No Google link
  fetching in this slice: the server would have to fetch URLs it's given, which needs SSRF care.
- **Parsing** finds the block headers (date cells like `Sun, 4 Oct`, with year and month from
  column B), takes slot columns from the numbered headers **up to `SubTot`, however many there
  are**, and player names from column A. `-` and blanks mean no entry. `SubTot`, `Total`, `Rank`
  and `Movement` are ignored, then recomputed.
- **Preview before anything is saved:**
  - players that will be created (names not found, matched case-insensitively);
  - play days that will be created, with their week labels;
  - number of entries per play day;
  - **mismatches** between the sheet's typed totals and the recomputed ones (e.g. the 10-point gap);
  - **odd values** to decide on: negative scores (`-1`: skip, or import as an extra −1) and
    scores above the season's target (`43`: keep).
  - If the play days fall outside the season's dates, an option to extend the season.
- **Confirm** writes everything in one transaction and records it in the audit log.
- **Re-running** is allowed: play days that already have entries are skipped unless the admin
  ticks "replace these play days".
- Limits: 2 MB, first sheet only, xlsx only. Parsed with Go's `archive/zip` and `encoding/xml`
  under size and row limits, so no new dependency; the parser was prototyped while analysing the sheet.

### 7.5 Season dates

Season start and end dates are already editable (*Seasons → edit*, date pickers), and saving
checks that every play day still falls inside. Add a plain-language summary next to the
pickers, e.g. "**Oktober minggu 1 – Desember minggu 4** (1 Okt – 28 Des)", using the same
week-of-month rule as §7.3, so "fourth week of December" is unambiguous.

## 8. Open questions

1. **Money:** keep fees/memberships out of tally, link tally to the cashflow app (both log in
   through aio), or add only a simple *paid* tick at check-in?
2. **Player details:** if we add level, WhatsApp, gender or age, they must be admin-only, never on
   the public board or poster. Do we need anything beyond level?
3. **Courts:** what upper limit do the clubs actually need?
4. **Players without accounts:** is the "tonight" view for anyone with the link, or should players
   log in (through aio) to see "my matches"?
5. **What does `-1` mean** in the sheet: a penalty, or a slot marked "not played"?
6. **Sanity cap** on matches per player per play day: none, or a warning above, say, 12?
7. **Is the sheet view public** (anyone with the link, like the board) or admin-only?
8. **Season end:** does "fourth week of December" end on the 28th, or run to the 31st
   (which is W5 under §7.3)?
9. **Points-only vs matches:** should points-only be a setting on the tally (hiding shuffle and
   courts), or should both always be on the play-day page?

## 9. Backlog

- **Link a player to a tally user** (`participants.user_id` already exists), so players can
  see "my matches" and get notified. From §6.7.
- Recording matches (pairs, winner) alongside points-only entries, for clubs that want it.
- The §5 directions, re-ranked after the first slice ships.

## 10. Next steps

- [x] Analyse the club's sheet (§4.2) and record decisions (§6).
- [ ] opan answers §8 questions 5–9.
- [ ] Build §7.1–7.2 (data + entry), then 7.3 (sheet view), then 7.4 (import), then 7.5.
- [ ] Import the October sheet into a test tally and compare totals with the sheet.
- [ ] Collect concrete feedback from the users who found tally complex, once points-only ships.
