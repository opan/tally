package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"html"
	"sort"
	"strconv"
	"testing"
	"time"
)

// buildXLSX writes a one-sheet workbook; text cells go through the shared
// strings table like a real export, numbers are stored as numbers.
func buildXLSX(t *testing.T, cells map[string]string) []byte {
	t.Helper()
	var shared []string
	idx := map[string]int{}
	type cell struct {
		row, col int
		ref, xml string
	}
	var list []cell
	for ref, v := range cells {
		r, c, ok := cellRef(ref)
		if !ok {
			t.Fatalf("bad ref %s", ref)
		}
		x := ""
		if _, err := strconv.ParseFloat(v, 64); err == nil {
			x = fmt.Sprintf(`<c r="%s"><v>%s</v></c>`, ref, v)
		} else {
			i, seen := idx[v]
			if !seen {
				i = len(shared)
				idx[v] = i
				shared = append(shared, v)
			}
			x = fmt.Sprintf(`<c r="%s" t="s"><v>%d</v></c>`, ref, i)
		}
		list = append(list, cell{r, c, ref, x})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].row != list[j].row {
			return list[i].row < list[j].row
		}
		return list[i].col < list[j].col
	})
	var sheet bytes.Buffer
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, c := range list {
		if i == 0 || list[i-1].row != c.row {
			if i > 0 {
				sheet.WriteString(`</row>`)
			}
			fmt.Fprintf(&sheet, `<row r="%d">`, c.row)
		}
		sheet.WriteString(c.xml)
	}
	if len(list) > 0 {
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	var sst bytes.Buffer
	sst.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		fmt.Fprintf(&sst, `<si><t>%s</t></si>`, html.EscapeString(s))
	}
	sst.WriteString(`</sst>`)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Points" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   sheet.String(),
		"xl/sharedStrings.xml":       sst.String(),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// clubSheet is shaped like the club's sheet: year in B2, a block per play day
// (slots 1–7, SubTot, Total, Rank), names in column A.
func clubSheet() map[string]string {
	c := map[string]string{"A1": "POINTS", "B2": "2026", "B3": "OCT"}
	block := func(first int, date string, label string) {
		c[colName(first)+"4"] = date
		c[colName(first+1)+"4"] = label
		for i := 0; i < 7; i++ {
			c[colName(first+i)+"5"] = strconv.Itoa(i + 1)
		}
		c[colName(first+7)+"5"] = "SubTot"
		c[colName(first+8)+"5"] = "Total"
		c[colName(first+9)+"5"] = "Rank"
	}
	block(2, "Sun, 4 Oct", "W1")    // B..K
	block(12, "Fri, 23 Oct", "W4A") // L..U
	block(22, "Sun, 2 Nov", "W5")   // V..AE: empty, a future week
	row := func(r int, name string, vals map[string]string) {
		c["A"+strconv.Itoa(r)] = name
		for k, v := range vals {
			c[k+strconv.Itoa(r)] = v
		}
	}
	// Ana: a deuce written 43 plus the -1 correction; SubTot includes the -1.
	row(6, "Ana", map[string]string{"B": "42", "C": "43", "D": "40", "H": "-1", "I": "124", "J": "124",
		"L": "42", "M": "-", "S": "42", "T": "166"})
	// Budi: a plain day, and a running Total that is 10 short on the sheet.
	row(7, "Budi", map[string]string{"B": "41", "C": "30", "I": "71", "J": "71",
		"L": "42", "M": "42", "S": "84", "T": "145"})
	row(8, "Citra", map[string]string{"I": "-", "J": "-"}) // never played
	row(9, "  Dewi   Sari ", map[string]string{"L": "35", "S": "35", "T": "35"})
	return c
}

func TestParsePointsSheet(t *testing.T) {
	g, err := readFirstSheet(buildXLSX(t, clubSheet()))
	if err != nil {
		t.Fatal(err)
	}
	if g.Name != "Points" {
		t.Errorf("sheet name %q", g.Name)
	}
	p := parsePointsSheet(g, 42, 1999)
	if len(p.Errors) > 0 {
		t.Fatalf("errors: %+v", p.Errors)
	}
	if len(p.Days) != 2 {
		t.Fatalf("got %d days, want 2 (2 Nov is empty)", len(p.Days))
	}
	if d := p.Days[0].Date; d != time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) {
		t.Errorf("first date %s, want 2026-10-04 (year from B2)", d)
	}
	if got := fmt.Sprint(p.Days[0].Players); got != "[{Ana [42 42 40]} {Budi [41 30]}]" {
		t.Errorf("4 Oct plays = %s", got)
	}
	if got := fmt.Sprint(p.Days[1].Players); got != "[{Ana [42]} {Budi [42 42]} {Dewi Sari [35]}]" {
		t.Errorf("23 Oct plays = %s", got)
	}
	if len(p.Capped) != 1 || p.Capped[0].Cell != "C6" || p.Capped[0].A != 43 {
		t.Errorf("capped = %+v", p.Capped)
	}
	if len(p.Ignored) != 1 || p.Ignored[0].Cell != "H6" {
		t.Errorf("ignored = %+v", p.Ignored)
	}
	// Ana's SubTot 124 = 42+43+40-1 matches what's imported (42+42+40).
	if len(p.SubTotal) != 0 {
		t.Errorf("subtotal notes = %+v", p.SubTotal)
	}
	if len(p.Total) != 1 || p.Total[0].Cell != "T7" || p.Total[0].A != 145 || p.Total[0].B != 155 {
		t.Errorf("total notes = %+v", p.Total)
	}
	if len(p.EmptyDays) != 1 || p.EmptyDays[0] != "Sun, 2 Nov" {
		t.Errorf("empty days = %v", p.EmptyDays)
	}
	if p.NeverPlayed != 1 {
		t.Errorf("never played = %d, want 1 (Citra)", p.NeverPlayed)
	}
}

func TestParsePointsSheetErrors(t *testing.T) {
	c := clubSheet()
	c["D7"] = "abc"
	c["A10"] = "ana"
	g, err := readFirstSheet(buildXLSX(t, c))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, e := range parsePointsSheet(g, 42, 2026).Errors {
		keys[e.Key] = true
	}
	if !keys["cell"] || !keys["dup_name"] {
		t.Errorf("errors %v, want cell and dup_name", keys)
	}
	if _, err := readFirstSheet([]byte("not a zip")); err == nil {
		t.Error("garbage read as xlsx")
	}
	g, _ = readFirstSheet(buildXLSX(t, map[string]string{"A1": "Hello"}))
	if p := parsePointsSheet(g, 42, 2026); len(p.Errors) != 1 || p.Errors[0].Key != "no_blocks" {
		t.Errorf("sheet without blocks: %+v", p.Errors)
	}
}

func TestParseSheetDate(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
	}{
		{"Sun, 4 Oct", "2026-10-04"}, {"Jum, 23 Okt", "2026-10-23"}, {"4 Oct 2027", "2027-10-04"},
		{"2026-11-01", "2026-11-01"}, {"46299", "2026-10-04"}, {"31 Nov", ""}, {"W1", ""},
	} {
		d, ok := parseSheetDate(c.in, 2026)
		got := ""
		if ok {
			got = d.Format(dateLayout)
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestImportScores(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "owner", "x")
	tl, err := s.CreateTally(ctx, u.ID, Tally{Slug: "club", Name: "Club", Template: "badminton_doubles", Visibility: VisibilityLink})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddParticipants(ctx, tl.ID, u.ID, []string{"Ana"}); err != nil {
		t.Fatal(err)
	}
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	period, _ := s.CreatePeriod(ctx, u.ID, Period{TallyID: tl.ID, Name: "S1", StartsOn: day(10), EndsOn: day(31), Scoring: DefaultContinuous})
	days := []ImportDay{
		{Date: day(4), Plays: []sheetPlays{{Name: "ana", Points: []int{42, 40, 41}}, {Name: "Budi", Points: []int{30}}}},
		{Date: day(23), Plays: []sheetPlays{{Name: "Budi", Points: []int{42, 42}}}},
	}
	res, err := s.ImportScores(ctx, tl.ID, u.ID, period, days, false, day(4), day(31), "points.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if res != (ImportResult{Days: 2, Entries: 6, NewPlayers: 1}) {
		t.Errorf("first import = %+v", res)
	}
	p, _ := s.PeriodByID(ctx, tl.ID, period.ID)
	if !p.StartsOn.Equal(day(4)) {
		t.Errorf("season starts %s, want widened to 4 Oct", p.StartsOn.Format(dateLayout))
	}
	rounds, _ := s.RoundsForPeriod(ctx, period.ID)
	if len(rounds) != 2 {
		t.Fatalf("%d play days", len(rounds))
	}
	scores, _ := s.ScoreAwards(ctx, rounds[1].ID) // 4 Oct (newest first)
	var order []int
	for _, a := range scores {
		if a.Name == "Ana" {
			order = append(order, a.Points)
		}
	}
	if fmt.Sprint(order) != "[42 40 41]" {
		t.Errorf("Ana's plays on 4 Oct in order %v, want the sheet's [42 40 41]", order)
	}

	// Again: both days have points now, so both are skipped...
	if res, err = s.ImportScores(ctx, tl.ID, u.ID, p, days, false, p.StartsOn, p.EndsOn, "points.xlsx"); err != nil || res.Skipped != 2 || res.Entries != 0 {
		t.Errorf("re-import = %+v, %v; want 2 skipped", res, err)
	}
	// ...unless replaced; extras stay.
	r23, _ := s.RoundByID(ctx, tl.ID, rounds[0].ID)
	players, _ := s.ListParticipants(ctx, tl.ID)
	if err := s.AddAwards(ctx, tl.ID, u.ID, r23, []AwardInput{{ParticipantID: players[0].ID, Points: 5}}, "bonus"); err != nil {
		t.Fatal(err)
	}
	if res, err = s.ImportScores(ctx, tl.ID, u.ID, p, days, true, p.StartsOn, p.EndsOn, "points.xlsx"); err != nil || res.Replaced != 2 || res.Entries != 6 {
		t.Errorf("replace = %+v, %v; want 2 replaced, 6 entries", res, err)
	}
	st, _ := s.StandingsAsOf(ctx, period.ID, time.Time{})
	total := 0
	for _, x := range st {
		total += x.Points
	}
	if total != 42+40+41+30+42+42+5 {
		t.Errorf("season total %d after replace, want 242 (scores once + the bonus)", total)
	}
}
