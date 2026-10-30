# RFC-002: Linking players to accounts

- **Status:** Draft; discussion recorded, decisions open (§8)
- **Author:** opan
- **Created:** 2026-10-30 · **Updated:** 2026-10-30
- **Scope:** every tally type; both login modes (`AUTH_PROVIDER=local` and `aio`)
- **Related:** RFC-001 §9 ("link a player to a tally user", backlog); `participants.user_id`
  (in the schema since P0, unused); NOTES.md "Players"

## Complexity score: **4 / 10**

One table for invites and use of the existing `participants.user_id`; an invite form on the
Pemain tab; a box for pending invites and a "my tallies as a player" list on the home page;
accept, decline and unlink; read access to private tallies for linked players; the guide.
No new dependencies. Email notifications (§8.3) would add to it.

## 1. Problem

Tally is run by admins. Players are names an admin types in (`participants`), not accounts,
and they follow along through the shared leaderboard link. Someone who signs up today lands
on *Tally saya*, which only lists tallies they **run**. A player who wants to see **their own
plays** in the mabar they join has no place in the app: they need the link from the group
chat, can't see a private tally at all, and nothing ties the name "Prabski" on a board to the
person who logged in.

Signing up must not mean becoming an admin. A player should be able to sign up only to follow
the tallies they play in.

## 2. Is it needed?

Not for what tally does today: admins enter everything, and the board and player pages are
open to anyone with the link (for link and public tallies). So linking stays **optional and
never required**: a tally works the same with no linked players, and a player never has to
sign up.

What it adds, for players who do sign up:

- **"My mabar" on the home page:** every tally they play in, with their rank, recent play days
  and points, without hunting for links.
- **Private tallies:** a linked player can see the tally they play in (§8.1).
- **A base for later:** a player's own history across seasons, notifications ("you're now
  #3"), maybe checking in or entering their own points.

## 3. Goals and non-goals

Goals:

- An admin can connect a player in their tally to a tally account, and the account's owner
  decides whether to accept.
- Nobody can use the feature to find out whether a username exists.
- A linked player sees their tallies and plays from their home page.
- Either side can undo a link.

Non-goals (for now):

- Players changing anything: their name, scores, or attendance stay with admins.
- Merging two players, or moving history between players.
- Public profiles; a player's account stays out of public pages (§8.2).

## 4. The proposal: invite by username (opan)

### 4.1 Flow

1. On the **Pemain** tab, an admin opens **Hubungkan ke akun** (link to an account).
2. They pick the **player** from an autocomplete field (the same `<datalist>` picker as the
   Poin tab) and **type the username** of the account. The username field is deliberately
   **not** an autocomplete: whoever types must know the username.
3. **Kirim** always answers the same way, for example "Undangan dikirim ke @x. Kalau akun itu
   ada, pemiliknya akan melihatnya di halaman utama Tally." It never says whether the
   username exists (§5.1).
4. The owner of that username sees the invite on their **home page** the next time they open
   tally: which tally, which player name, and who invited them. They choose **Terima**
   (accept) or **Tolak** (decline).
5. On accept, the player is linked to the account (`participants.user_id`), and the tally
   shows up in the user's list of tallies they play in.

### 4.2 What each side sees

| | Admin (Pemain tab) | Invited user (home page) |
|---|---|---|
| Pending | "Undangan dikirim ke @x" on the player row: only what the admin typed; can cancel it | The invite, with Terima / Tolak |
| Accepted | "Terhubung: @x" on the player row; can unlink | The tally under "Mabar saya"; can unlink |
| Declined or expired | Back to no invite, the same as a username that doesn't exist | Nothing |

A declined invite looks exactly like one sent to a username that doesn't exist, so declining
reveals nothing either.

### 4.3 Players need to know their username

The admin has to type the player's **tally** username. With aio login, the tally username is
made from the aio account name (`localUsername`: lowercased, other characters turned into
`_`, sometimes with a suffix when the name was taken), so players may not know it.

The header already shows the username at the top right, but not well enough for this:
on phones (640px and narrower) it's hidden to make room, and on wider screens it's cut off
after about 14 characters. So the header should **show the full username on phones too**
(small, on its own row or in a compact menu, never truncated without a way to see all of it).
That's enough for a player to read it and send it to the admin; no separate line on the home
page is needed (opan, 2026-10-30).

## 5. Safety

### 5.1 No username lookup

- The submit **doesn't look the username up**. It stores the invite with the typed
  username, normalized the way usernames are (`normalizeUsername`). The match happens when a
  user opens their home page: invites whose username equals theirs. So neither the answer nor
  the response time depends on whether the account exists.
- The admin's view only ever shows what the admin typed, until the user accepts. Accepting
  does reveal that the account exists, to that tally's admins, and that's the user's choice.
- There's no list or search of usernames anywhere for admins.

### 5.2 Abuse and mistakes

- **Spam:** a cap on invites per tally per day (e.g. 30) and per player pending at once (one,
  §8.5). Invites **expire** after 14 days.
- **Unwanted invites:** besides Tolak, the user can choose **Abaikan tally ini** (ignore this
  tally): later invites from that tally aren't shown to them. The admin can't tell.
- **Wrong person:** the invite names the tally, the player and the inviting admin, so the user
  can see whether it's really them before accepting. Both sides can unlink later.
- **One-to-one:** a player is linked to at most one account, and an account to at most one
  player per tally (a unique index on `(tally_id, user_id)`). An account can be linked in any
  number of tallies.
- **Account deleted:** `participants.user_id` is `ON DELETE SET NULL` already; the player and
  their points stay.
- Everything goes through the audit log (invite sent, cancelled, accepted, declined,
  unlinked), like other changes.

## 6. Alternatives considered

1. **An invite link per player.** The admin taps *Undang* on a player and gets a link to paste
   into the group chat; whoever opens it while logged in claims that player. No usernames
   needed, which fits how clubs talk. A forwarded link could be claimed by the wrong person,
   so it must be single-use, expire, show the admin who claimed it, and be easy to undo.
2. **The player asks, an admin approves.** On a link or public tally, a logged-in user taps
   *Ini saya* (that's me) on a player page; an admin approves it on the Pemain tab. Nobody
   needs to know anything in advance, and approval stops impersonation. Doesn't work for
   private tallies, which the user can't see.
3. **Automatic matching by name.** Rejected: names are typed by admins, differ in spelling,
   and anyone could pick a matching username.

The username invite (§4) is the simplest and has no enumeration risk, so it comes first.
Alternative 2 complements it later, because it doesn't need the admin to know usernames.

## 7. Design sketch

### 7.1 Data

```sql
CREATE TABLE player_invites (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tally_id       uuid        NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    participant_id uuid        NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    username       text        NOT NULL,          -- as typed, normalized; never resolved on submit
    invited_by     uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    expires_at     timestamptz NOT NULL,
    status         text        NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled', 'expired'))
);
-- one pending invite per player (§8.5), found by username on the home page
CREATE UNIQUE INDEX ON player_invites (participant_id) WHERE status = 'pending';
CREATE INDEX ON player_invites (username) WHERE status = 'pending';

-- one player per account per tally
CREATE UNIQUE INDEX ON participants (tally_id, user_id) WHERE user_id IS NOT NULL;

-- "Abaikan tally ini"
CREATE TABLE tally_mutes (
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tally_id uuid NOT NULL REFERENCES tallies(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, tally_id)
);
```

Accepting sets `participants.user_id` and the invite's status in one transaction, and fails
cleanly if the player was linked or the account already has a player in that tally meanwhile.

### 7.2 Pages

- **Pemain tab:** *Hubungkan ke akun* (player picker + username) and, on each player row, the
  link state: none, "undangan dikirim ke @x" (cancel), or "terhubung: @x" (unlink).
- **Home page** (`/` when logged in, today *Tally saya*):
  - a box of pending invites with Terima, Tolak and Abaikan tally ini;
  - **Mabar saya**: tallies where the user is a linked player, each with their rank and
    points in the current season and a link to their player page;
  - *Tally saya* (tallies they run) stays as it is.
- **Header:** the full username, on phones too (§4.3).
- **Player page:** when the viewer is that linked player, a small "Ini kamu" and *Putuskan*
  (unlink).
- **Private tallies:** `loadTally` also lets in a user linked to a player in that tally,
  read-only (§8.1).

### 7.3 Unchanged

Admins keep full control of names, scores and attendance; linking changes nothing on the
board, poster or totals. Tally admins and linked players are separate: an admin can also be a
linked player in the same tally.

## 8. Open questions

1. **Private tallies:** can a linked player see the board and player pages of a private tally
   they play in? Proposed: yes, read-only; linking makes them a member.
2. **Showing the account:** does a linked player's username appear anywhere public, or only to
   that tally's admins? Proposed: only to admins.
3. **Notifications:** only on the home page, or also by email? aio knows users' email
   addresses, but tally would need aio to send it, or its own mail setup.
4. **"Mabar" in the UI:** use the word (*Mabar saya*), or keep "tally" everywhere?
5. **Re-inviting:** when a player has a pending invite and the admin sends another to a
   different username, does the new one replace the old one? Proposed: yes, one pending
   invite per player.
6. **Limits:** 14 days to expire and 30 invites per tally per day, or other numbers?

## 9. Backlog (after this)

- Alternative 2 (*Ini saya* with admin approval) for link and public tallies.
- A player's own page across all their tallies and seasons.
- Notifications beyond the home page (§8.3).

## 10. Next steps

- [ ] opan answers §8.
- [ ] Build §7: invites and linking, the home page, private access, the guide.
