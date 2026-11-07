# RFC-004: Player payments per play day

- **Status:** Draft; §9.1, §9.2 and §9.5 answered (opan, 2026-11-07), §9.3, §9.4 and §9.6 open
- **Updated:** 2026-11-07
- **Author:** opan
- **Created:** 2026-11-05
- **Scope:** every tally; both login modes. Phase 2 also touches cashflow.
- **Related:** Deck card 358; RFC-003 (play days belong to a leaderboard); cashflow
  (`~/cashflow`, cashplans with append-only entries)

## Complexity score: **5 / 10** (phase 1), **+3** for the cashflow link (phase 2)

Phase 1 is one table, a fee per play day, a form and a list on the play day page, and a page to
read the history. The cashflow link is smaller in code but crosses two apps, needs a token between them,
and has to live with cashflow never deleting or changing an amount.

## 1. Problem

Each mabar, the club asks every player for a fixed fee (say Rp50.000) that covers everything:
court, shuttlecocks, drinks. It doesn't matter how many games someone plays. Some players play
for free that day, at an admin's call (a coach, a birthday, a guest).

Today this is tracked outside tally, on paper or in a chat. Nobody can easily see who has paid
for which play day, or who still owes.

## 2. Goals and non-goals

**Goals**

- Each play day has its **fee** (Rp50.000), set by an admin.
- Admins record payments **one at a time**, as people pay: pick the player, pick how they paid
  (**Tunai**, **Transfer** or **Gratis**), submit. This replaces the list they keep by hand today.
- **Everyone** who can see the tally can see the history: who paid, how, and for which play day.
- A club that never sets a fee sees nothing new.
- Optionally (phase 2), the money also lands in the club's cashflow cashplan, so the cash book
  has it without typing it twice.

**Non-goals (for now)**

- Partial payments, credit, or paying ahead for several play days. A payment is always the full
  fee of one play day.
- Receipts or proof of transfer. A short note covers "BCA, a.n. istri".
- Working out who *should* have paid (from points or attendance) and listing who owes. Admins
  record who paid, as on their list today (§3, 2026-11-07). Could come later (§10).
- Online payment (QRIS, payment links).
- Fees that differ per player (members vs guests). Gratis covers the common exception.
- Monthly or yearly dues: cashflow already has *Iuran* for that.

## 3. What opan said (2026-11-05)

1. The fee covers everything and is the same for everyone, however many games they play
   (example: Rp50.000 per mabar).
2. An admin can give a **free pass**.
3. Only admins record payments; **everyone can see the history**.
4. Partial payment is rare: assume a payment is always the full fee.
5. Payments link to a **play day**.
6. Link to cashflow if doable; if not, tally keeps it itself.

And on 2026-11-07:

7. **Gratis is a payment option**, next to cash and transfer. The fee is set for **each play day**.
8. **Admins enter payments one by one:** pick the player (a name field with autocomplete), pick
   the payment option, submit. Doing it per player is fine. Today they keep a list by hand and
   update it whenever someone pays.

## 4. Model

```
rounds.fee  bigint  NOT NULL DEFAULT 0  -- whole rupiah per player for this play day; 0 = not set

payments(
    id             uuid        PK,
    round_id       uuid        NOT NULL → rounds(id),
    participant_id uuid        NOT NULL → participants(id),
    method         text        NOT NULL CHECK (method IN ('cash', 'transfer', 'free')),
    note           text        NOT NULL DEFAULT '',
    recorded_by    uuid        NOT NULL → users(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (round_id, participant_id)
)
```

- **Fee on the play day.** Each mabar has its own fee (§3.7). The field starts with the previous
  play day's fee on the same leaderboard, so in practice it's typed once and then just confirmed.
- **Amount follows from the play day.** A payment is always the full fee (§3.4), so it doesn't
  store an amount. Tunai and Transfer count as the day's fee; Gratis counts as zero.
  - Collected that day: (Tunai + Transfer) × fee.
  - Fixing a play day's fee after payments are in recalculates that day, which is what fixing a
    typo should do. The change is audited.
- **One payment per player per play day.** Recording a second one for the same player and day
  says they're already recorded, and how.
- **Undo.** An admin can undo a payment (a wrong tap, the wrong player). Every record and undo
  goes in `audit_log`, like other admin actions.
- **Deleting a player** who has payments is refused. Deactivating them still works, as with
  points. Play days can't be deleted today, so payments don't block anything else.

## 5. Pages

### 5.1 Recording (play day page, admins)

A new card **Iuran** on the play day page, after the points:

```
Iuran                                         Rp [ 50.000 ]  [Simpan]
──────────────────────────────────────────────────────────────────
Pemain   [ Prab…            ▾ ]   (autocomplete, same list as Poin)
Bayar    ( Tunai ) ( Transfer ) ( Gratis )
Catatan  [ opsional              ]
                                                         [Catat]
──────────────────────────────────────────────────────────────────
Sudah tercatat · 18 tunai · 3 transfer · 2 gratis · Rp1.050.000
Prabski        Tunai       19.42                       [Batalkan]
Jacque Oui     Gratis      19.40   pelatih             [Batalkan]
Bahlul         Transfer    19.35                       [Batalkan]
…
```

- **The form asks for the fee first.** Until the play day has one, the form is hidden behind
  "Isi iuran hari ini".
- **The form stays open after each submit**, with the player field empty and focused, so the
  next person who hands over cash is quick to add.
- **Newest first,** so the last entry is easy to check and undo.
- **Earlier play days stay open:** someone paying a week late is recorded on the play day they
  pay for.

### 5.2 Reading (everyone)

- **New tab "Iuran"**, visible to everyone once a play day has a fee. Play days are shown newest
  first, each with its fee and totals (tunai, transfer, gratis, Rp), and expand to show names
  and how each paid.
- **Player page:** an *Iuran* section listing the play days they paid for and how.
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
| **B. One entry per play day** *(recommended)* | Admin taps **Catat ke cashflow** on the play day. Tally adds one income entry: "Mabar Minggu 2 Nov · 21 × Rp50.000 (18 tunai, 3 transfer; 2 gratis)", the total, dated on the play day. | One readable line per mabar. Few rows that can never be undone. | Per-player detail stays in tally. |
| **C. One entry per payment** | Each Tunai or Transfer payment sends its own income entry, with the player as payer. | Cashflow's per-payer totals work. | ~25 rows per mabar, and a wrong tap is a permanent entry in cashflow. |
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

1. **Schema:** `rounds.fee`, the `payments` table, and an index on
   `(participant_id, round_id)`.
2. **Store:** set a play day's fee, record and undo a payment, list per play day and per player;
   audit entries.
3. **Pages:** the Iuran card on the play day page, the Iuran tab and the player page section.
4. **Metrics:** `tally_payments_total{method}` and a gauge for the amount collected. Grafana
   panel in homelab.
5. **Guide:** a section on Iuran, with screenshots. **Tests:** store, handlers, and pages that
   render with and without a fee.

### Phase 2: cashflow link (option B)

1. **cashflow PR:** link tokens per cashplan and `POST /api/v1/plans/{slug}/entries` with an
   idempotency key; tests; README.
2. **tally PR:** the Cashflow card in Setelan, "Catat ke cashflow" on the play day card, and the
   sent/unsent state per payment.

Phase 1 is useful on its own and can ship first. Phase 2 waits for opan's call on §9.6.

## 8. Risks

- **Public history.** For a tally that anyone with the link can see, who paid and how is public.
  That's what opan asked for. A club that doesn't want it can make the tally private.
- **Someone is missed.** Tally doesn't list who still owes (§2), so it catches a missed payment
  no better than the list on paper does. §10 has the follow-up if that turns out to matter.
- **Money is permanent in cashflow** (phase 2). This is the reason for one deliberate send per
  play day rather than one per tap.
- **A token between two apps** (phase 2). It's scoped to one cashplan and can only add income
  entries. The owner can revoke it in cashflow.

## 9. Questions for opan

1. ~~**Free pass scope.**~~ Answered 2026-11-07: Gratis is a payment option on a play day,
   next to Tunai and Transfer.
2. ~~**Who should pay.**~~ Answered 2026-11-07: admins enter each payment by hand, and tally
   doesn't work out who should pay.
3. **Where the history lives.** A new **Iuran** tab for everyone (proposed), or only on player
   pages?
4. **Undo.** Admins can undo a payment any time, audited (proposed), or only on the same day?
5. ~~**Fee per leaderboard or club.**~~ Answered 2026-11-07: per play day, prefilled from the
   previous one.
6. **Cashflow.** Option **B** (one entry per play day, proposed), or C, D, or A (none)? And which
   cashplan: is there one for the club already?

## 10. Later, if a club asks

- **Who still owes:** compare the payments with who has points or attendance on the play day,
  and show "maybe not paid yet" to admins only.

## 11. Next steps

- [x] opan answers §9.1, §9.2, §9.5 (2026-11-07).
- [ ] opan answers §9.3, §9.4, §9.6.
- [ ] Phase 1 PR.
- [ ] Phase 2: cashflow PR, then tally PR (if §9.6 isn't A).
