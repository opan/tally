# RFC-004: Player payments per play day

- **Status:** Draft, for opan to review (§9)
- **Author:** opan
- **Created:** 2026-11-05
- **Scope:** every tally; both login modes. Phase 2 also touches cashflow.
- **Related:** Deck card 358; RFC-003 (play days belong to a leaderboard); cashflow
  (`~/cashflow`, cashplans with append-only entries)

## Complexity score: **5 / 10** (phase 1), **+3** for the cashflow link (phase 2)

Phase 1 is one table, a fee setting, a card on the play day page and a page to read the
history. The cashflow link is smaller in code but crosses two apps, needs a token between them,
and has to live with cashflow never deleting or changing an amount.

## 1. Problem

Each mabar, the club asks every player for a fixed fee (say Rp50.000) that covers everything:
court, shuttlecocks, drinks. It doesn't matter how many games someone plays. Some players play
for free that day, at an admin's call (a coach, a birthday, a guest).

Today this is tracked outside tally, on paper or in a chat. Nobody can easily see who has paid
for which play day, or who still owes.

## 2. Goals and non-goals

**Goals**

- Admins record, per play day, that a player **paid** (the full fee) or plays **free**.
- **Everyone** who can see the tally can see the history: who paid, who was free, who hasn't
  paid yet, per play day and per player.
- The fee is set once and stays the same until an admin changes it. A change never rewrites
  old payments.
- A club that doesn't set a fee sees nothing new.
- Optionally (phase 2), the money also lands in the club's cashflow cashplan, so the cash book
  has it without typing it twice.

**Non-goals (for now)**

- Partial payments, credit, or paying ahead for several play days. A payment is always the full
  fee of one play day.
- Payment methods, receipts or proof of transfer. A short note covers "transfer BCA".
- Online payment (QRIS, payment links).
- Fees that differ per player (members vs guests). Free covers the common exception.
- Monthly or yearly dues: cashflow already has *Iuran* for that.

## 3. What opan said (2026-11-05)

1. The fee covers everything and is the same for everyone, however many games they play
   (example: Rp50.000 per mabar).
2. An admin can give a **free pass**.
3. Only admins record payments; **everyone can see the history**.
4. Partial payment is rare: assume a payment is always the full fee.
5. Payments link to a **play day**.
6. Link to cashflow if doable; if not, tally keeps it itself.

## 4. Model

```
leaderboards.fee   bigint  NOT NULL DEFAULT 0  -- whole rupiah per player per play day; 0 = off

payments(
    id             uuid        PK,
    round_id       uuid        NOT NULL → rounds(id),
    participant_id uuid        NOT NULL → participants(id),
    kind           text        NOT NULL CHECK (kind IN ('paid', 'free')),
    amount         bigint      NOT NULL,         -- the fee when recorded; 0 for free
    note           text        NOT NULL DEFAULT '',
    recorded_by    uuid        NOT NULL → users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (round_id, participant_id)
)
```

- **Fee on the leaderboard.** A play day belongs to a season, and a season to a leaderboard
  (RFC-003), so badminton and futsal in one club can ask different fees. Most clubs have one
  leaderboard, and the setting looks like "the club's fee".
- **Amount copied onto each payment.** Raising the fee to Rp60.000 in December leaves
  October's payments at Rp50.000.
- **One row per player per play day.** "Paid" and "free" exclude each other. **Not paid** is the
  absence of a row.
- **Who should pay that day:** every player with points, a match or attendance on that play day.
  An admin can also record a payment for an active player who came but has no entry yet.
- **Undo.** An admin can undo a payment or free pass (a wrong tap). Every record and undo goes
  in `audit_log`, like other admin actions.
- **Deleting a player** who has payments is refused. Deactivating them still works, as with
  points. Play days can't be deleted today, so payments don't block anything else.

## 5. Pages

### 5.1 Setting the fee (Setelan → Papan)

Each leaderboard row gets **Iuran per main**: an amount in rupiah, empty = off. With one
leaderboard, the card reads as the club's fee. Changing it asks for confirmation and says old
payments keep their amount.

### 5.2 Recording (play day page, admins)

A new card **Iuran · Rp50.000** on the play day page, after the points:

```
Iuran · Rp50.000                         21 lunas · 3 gratis · 2 belum
──────────────────────────────────────────────────────────────────
Prabski        Lunas ✓                        [Batalkan]
Jacque Oui     Gratis · pelatih               [Batalkan]
Bahlul         Belum      [Lunas]  [Gratis…]
Mas Gembul     Belum      [Lunas]  [Gratis…]
+ Tambah pemain yang belum tercatat main
──────────────────────────────────────────────────────────────────
Terkumpul Rp1.050.000
```

- One tap marks **Lunas**. **Gratis…** asks for an optional reason.
- Players who haven't paid come first, so the list shrinks as the evening goes on.
- Earlier play days stay open for payment: someone paying a week late is recorded on the day
  they owe for.

### 5.3 Reading (everyone)

- **New tab "Iuran"**, visible to everyone once a leaderboard has a fee. It shows play days
  newest first, each with its totals (Rp, lunas, gratis, belum), and expands to show names.
  It also has a short **"Belum bayar"** list at the top: players who owe for any play day this
  season, with the days.
- **Player page:** an *Iuran* section with each play day and Lunas / Gratis / Belum.
- **Site admin** (`/admin`): totals per tally, read-only.

## 6. Cashflow (phase 2)

### 6.1 What cashflow has today

- A **cashplan** is a shared cash book at `/p/{slug}`. Its **entries** are income (payer, amount,
  date, note) or expense.
- Entries are **append-only**, enforced in the database: never deleted, and the amount and type
  never change. Payer, note and date can be edited, with history kept.
- *Iuran* tracks monthly or periodic dues against a roster, by matching the payer's name.
- It logs in through aio, like tally. It has **no API**: every change goes through its forms.

So the link is doable, but cashflow needs a small API first.

### 6.2 Options

| | How | For | Against |
|---|---|---|---|
| **A. Tally only** | No link. Tally is the record. | Nothing to build in cashflow. | The cash book is typed in by hand. |
| **B. One entry per play day** *(recommended)* | Admin taps **Catat ke cashflow** on the play day. Tally adds one income entry: "Mabar Minggu 2 Nov · 21 × Rp50.000 (3 gratis)", the total, dated on the play day. | One readable line per mabar. Few rows that can never be undone. | Per-player detail stays in tally. |
| **C. One entry per payment** | Each Lunas sends its own income entry, with the player as payer. | Cashflow's per-payer totals work. | ~25 rows per mabar, and a wrong tap is a permanent entry in cashflow. |
| **D. Prefilled form** | A link opens cashflow's entry form already filled in. The admin saves it there. | No API, no token. | Easy to forget, and tally can't tell whether it was saved. |

### 6.3 How B works

- **Cashflow:**
  - The cashplan owner creates a **link token** (Kelola → Integrasi), shown once.
  - New endpoint `POST /api/v1/plans/{slug}/entries`, which accepts that token and creates an
    income entry.
  - Each entry carries a key that tally makes up (`tally:{round_id}:{n}`), so a retry never adds
    the same entry twice.
- **Tally:**
  - Setelan gets a **Cashflow** card: the cashplan link and the token. The token is stored
    server-side and never shown again.
  - The play day card shows **Catat ke cashflow · Rp1.050.000** for payments not sent yet, and
    after that **Tercatat di cashflow** with a link to the entry.
- **Late payments** after the play day was sent show up as "Rp50.000 belum dicatat". Another tap
  sends them as a second entry, "susulan".
- **Undo after sending:** cashflow can't take the money back out. Tally shows the difference
  ("Rp50.000 lebih di cashflow") and the admin corrects it in cashflow with an expense entry. This
  should be rare, because sending is a deliberate step once the evening's payments are in.

## 7. Plan

### Phase 1: payments in tally

1. **Schema:** `leaderboards.fee`, the `payments` table, and an index on
   `(participant_id, round_id)`.
2. **Store:** record, undo and list per play day, per player, and "owes" per season; audit
   entries.
3. **Pages:** the fee in Setelan → Papan, the card on the play day page, the Iuran tab and the
   player page section.
4. **Metrics:** `tally_payments_total{kind}` and a gauge for amount collected. Grafana panel in
   homelab.
5. **Guide:** a section on Iuran, with screenshots. **Tests:** store, handlers, pages render with
   and without a fee.

### Phase 2: cashflow link (option B)

1. **cashflow PR:** link tokens per cashplan and `POST /api/v1/plans/{slug}/entries` with an
   idempotency key; tests; README.
2. **tally PR:** the Cashflow card in Setelan, "Catat ke cashflow" on the play day card, and the
   sent/unsent state per payment.

Phase 1 is useful on its own and can ship first. Phase 2 waits for opan's call on §9.6.

## 8. Risks

- **Showing who hasn't paid.** For a tally that anyone with the link can see, "belum bayar" is
  public. That's what opan asked for. A club that doesn't want it can make the tally private.
- **Who "played".** A player who came but has no points or attendance yet won't be on the list
  until an admin adds them (§5.2, "Tambah").
- **Money is permanent in cashflow** (phase 2). This is the reason for one deliberate send per
  play day rather than one per tap.
- **A token between two apps** (phase 2). It's scoped to one cashplan and can only add income
  entries. The owner can revoke it in cashflow.

## 9. Questions for opan

1. **Free pass scope.** Per play day only (proposed), or also a standing free pass for a player
   (a coach who never pays)?
2. **Who should pay.** Everyone with points, a match or attendance that day (proposed)?
3. **Where the history lives.** A new **Iuran** tab for everyone (proposed), or only on player
   pages and inside Musim?
4. **Undo.** Admins can undo a payment any time, audited (proposed), or only on the same day?
5. **Fee per leaderboard** (proposed), or one fee for the whole club?
6. **Cashflow.** Option **B** (one entry per play day, proposed), or C, D, or A (none)? And which
   cashplan: is there one for the club already?

## 10. Next steps

- [ ] opan answers §9.
- [ ] Phase 1 PR.
- [ ] Phase 2: cashflow PR, then tally PR (if §9.6 isn't A).
