# RFC-006: Database (the member list)

- **Status:** Accepted (opan, 2026-11-19). Every question is answered (§3, §9); PR 1 is next.
- **Updated:** 2026-11-19
- **Author:** opan
- **Created:** 2026-11-19
- **Scope:** every tally; both login modes
- **Related:** RFC-005 step 2 (PR #31); RFC-004 (Iuran: a member and guest price is the likely
  next ask); RFC-005 steps 4 to 6 (tournament categories need gender, draws need level)

## Complexity score: **5 / 10**

Three optional columns on `participants`, a list of levels on the tally, and filters on a page
that already exists. The import reuses the points import's upload and preview. The work is in
keeping the new fields off every page that isn't for admins.

## 1. Problem

The club asked for a **Database** menu: the member list with what the admins know about each
player. Today a player in tally is a name, active or not, and maybe a linked account. The admins
keep the rest in a spreadsheet or in their heads: who is strong, who is a member and who comes as
a guest.

The later steps of RFC-005 need it too:

- tournament categories (MD, WD, XD) need gender;
- a fair draw or shuffle needs level;
- a member and guest price for Iuran needs to know who is a member.

## 2. Goals and non-goals

**Goals**

- Admins can record each player's **level**, **gender** and whether they're a **member or a
  guest**. All three are optional; a player with none looks as today.
- The player list can be **searched** by name and **filtered** by those three fields.
- An admin can set the level or the status of **many players at once**.
- A club can **import** its member list from Excel instead of typing it.

**Non-goals**

- Phone numbers, notes or any contact detail (§3.1).
- Showing any of the three fields to members (§3.3).
- Using the fields anywhere yet: different fees for guests (RFC-004 follow-up), tournament
  categories (RFC-007 to RFC-009) and balancing the shuffle by level (§7, PR 3, optional) come
  later.

## 3. Decided (opan, 2026-11-19)

1. **Fields:** level, gender, and member or guest. No phone and no note: fewer fields, less
   personal data.
2. **Level scale:** each tally sets its own ordered list. A new list is prefilled with
   `A+, A, B+, B, C`.
3. **Who sees levels:** the tally's admins only.
4. **Papan becomes "Event"** in the UI (RFC-005 §5.3). That's step 3, not this RFC, but the menu
   names in §5 assume it.

## 4. Model

```
participants
  + level       text NOT NULL DEFAULT ''   -- one of tallies.levels, or '' (not set)
  + gender      text NOT NULL DEFAULT ''   CHECK (gender IN ('', 'm', 'f'))
  + membership  text NOT NULL DEFAULT ''   CHECK (membership IN ('', 'member', 'guest'))

tallies
  + levels      text[] NOT NULL DEFAULT '{}'   -- ordered, strongest first; empty = not set up
```

- **Level as text, not an id.** The list is short and owned by the tally. Renaming a level in
  Setelan renames it on every player in the same transaction. Removing a level that players
  have asks first ("12 pemain punya level B; level mereka dikosongkan") and then clears it.
- **Limits:** up to 10 levels, each up to 10 characters, no duplicates (case-insensitive).
- **Gender** is stored as `m`/`f` and shown as **L**/**P** (Laki-laki/Perempuan) in Indonesian
  and **M**/**F** in English.
- **Audit:** every change to these fields, single or bulk, goes in `audit_log` with the before
  and after values, like a rename does today.
- **Upgrade:** `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` with defaults, so existing players get
  empty values and nothing changes until an admin fills them in.

## 5. Pages

### 5.1 Pemain (admins only, as today)

The page stays where it is, and stays admin-only. It gets:

- **Badges** after each name: level, L/P, Tamu (members get no badge, so the usual case stays
  quiet). Empty fields show nothing.
- **Search and filters** at the top: a name box, and Level, Gender and Status pickers. They're a
  plain `GET` form (`?q=&level=&gender=&status=`), so a filtered list can be bookmarked and needs
  no JavaScript. The count says "8 dari 46 pemain".
- **Edit** (the existing per-player menu) gets three pickers next to the rename field.
- **Bulk edit:** a checkbox on each row and a bar at the bottom: "Set level ▾ / Set status ▾ for
  5 selected". With JavaScript off, the checkboxes and the bar still work as one form.
- **Adding players:** the add form gets "Tambah sebagai: Anggota / Tamu / (kosong)", since a
  batch from a chat is usually one or the other.

### 5.2 Setelan (admins)

A **Level** card: the ordered list, with add, rename, move up/down and remove. A tally that never
opens it has no levels, and the Level picker and filter don't show.

### 5.3 Import (admins)

"Impor dari Excel" on Pemain, reusing `xlsx.go` and the upload → preview → confirm flow of the
points import:

- **Columns**, found by header name in Indonesian or English: Nama (required), Level, Gender or
  L/P, Status (Anggota/Member, Tamu/Guest). Other columns are ignored.
- **Matching** is by name, the same way adding players matches today (case and spaces ignored).
- **Preview** lists new players, changes to existing ones (old → new), and rows it can't use (an
  unknown level, an unknown gender word) with the reason. An empty cell never clears a value.
- **Confirm** applies it in one transaction, audited as one bulk change.

### 5.4 Everywhere else

The fields are **not** shown on public or member pages: Ringkasan, player pages, play day pages,
Iuran, the poster and its image, and the site admin pages. Tests check each of
these renders without them (§8).

## 6. Metrics

- `tally_players{membership}`: a gauge of active players by status (`member`, `guest`, `unset`)
  across all tallies. Counts only, never names or levels.
- A Grafana panel in homelab next to the existing tally panels.

## 7. Plan

1. **PR 1: fields and editing.** Schema, store, the per-player pickers, the Level card in
   Setelan, badges, "Tambah sebagai", audit, metrics, locales, guide section and screenshots,
   tests (including §5.4).
2. **PR 2: finding and bulk.** Search, filters, bulk edit, and the Excel import.
3. **PR 3 (optional): balance by level.** The shuffle on Main can pair strong with weak when
   levels are set. Built only if the club asks after using levels for a while.

## 8. Risks

- **Personal data.** Gender and a level are still personal, and a level can hurt if it leaks.
  Mitigation: admin-only pages, tests on every member-facing page and the poster, nothing in metrics
  but counts.
- **A long Pemain page.** Clubs with 100+ names get a busy page. Mitigation: the filters, and the
  badges stay short.
- **Import overwrites.** Mitigation: the preview shows every change, empty cells never clear, and
  the whole import is audited.

## 9. Questions for opan (answered 2026-11-19)

1. **Menu name:** keep **Pemain**. The club's "Database" is this page; the name doesn't change.
2. **Gender labels:** **L/P** (Laki-laki/Perempuan) in Indonesian, **M/F** in English, as in §4.

## 10. Next steps

- [x] opan answers §9.
- [ ] PR 1, then PR 2.
- [ ] PR 3 only if the club asks.
