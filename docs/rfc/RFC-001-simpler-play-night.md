# RFC-001: A simpler play night

- **Status:** Draft; collecting user feedback and opan's ideas (§5.4, §7)
- **Author:** opan
- **Created:** 2026-10-29
- **Scope:** badminton first; other tally types keep working but are not designed for here
- **Related:** NOTES.md (P2 play-day notes), the in-app guide `/panduan#hari-main`

## Complexity score: **TBD**

Depends on the slice picked in §7. The live-night items in §5.1 touch the schema
(named courts, match queue, check-in times) and replace the play-day page.

## 1. Problem

Early feedback from club members using tally: **the UX still feels too complex.**
We want people to use tally *during* the play night and keep coming back to the board,
not to treat it as a chore one admin does afterwards.

What we don't know yet (to be gathered, see §7):

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
  This overlaps the separate cashflow app (§6, open question 1).
- Non-badminton tally types get no new features in this RFC.
- Native mobile apps.

## 4. Reference: BadminHub

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

## 5. Candidate directions

Not decided. A starting list from §1.1 and §4, to be merged with opan's ideas (§5.4) and
ranked once the user feedback in §7 is in.

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
9. **Player import** from CSV/XLSX for clubs moving in with a list.

### 5.3 For players (engagement)

10. **Tonight view** on the public link: who's on court, who's next, results so far.
11. **"My matches"**: a player's own results and rank movement after each night.
12. **Level-balanced pairing** so matches feel fair (needs a level per player).
13. **Shuttlecock count** and a night recap (attendance, matches, shuttles).

### 5.4 opan's ideas

_To be added._

## 6. Open questions

1. **Money:** keep fees/memberships out of tally, link tally to the cashflow app (both log in
   through aio), or add only a simple *paid* tick at check-in?
2. **Player details:** if we add level, WhatsApp, gender or age, they must be admin-only, never on
   the public board or poster. Do we need anything beyond level?
3. **Courts:** what upper limit do the clubs actually need?
4. **Players without accounts:** is the "tonight" view for anyone with the link, or should players
   log in (through aio) to see "my matches"?

## 7. Next steps

- [ ] Collect concrete feedback: who said it, which screen, what they tried to do (a few
      sentences each is enough).
- [ ] opan adds their ideas to §5.4.
- [ ] Rank §5 against the feedback; pick a first slice (likely 5.1).
- [ ] Sketch the night screen on phone width before building it.
