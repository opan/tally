package main

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Importing a points sheet like the club's "BALA BULU POINT" spreadsheet
// (RFC-001 §7.4): player names in column A; per play day a block of slot
// columns 1..7 followed by SubTot (and Total, Rank, Movement), with the date
// in the row above the slot numbers, e.g. "Sun, 4 Oct". The year comes from
// a cell above the blocks (B2: 2026), else from the season.

// sheetNote is something the preview shows about one cell.
type sheetNote struct {
	Cell   string // e.g. "C20"
	Player string
	Date   time.Time
	A, B   int // values, meaning depends on the list (sheet vs imported, from vs to)
}

// sheetDay is one play day block of the sheet.
type sheetDay struct {
	Date    time.Time
	Header  string // the date cell as written
	Players []sheetPlays
}

type sheetPlays struct {
	Name   string
	Points []int // in slot order
}

// sheetParse is what came out of a sheet, before comparing with the tally.
type sheetParse struct {
	Days        []sheetDay
	Capped      []sheetNote // A = as written, B = imported (the target)
	Ignored     []sheetNote // -1 deuce corrections
	SubTotal    []sheetNote // A = sheet SubTot, B = sum imported
	Total       []sheetNote // A = sheet Total, B = previous Total + SubTot on the sheet
	EmptyDays   []string    // headers of blocks with no scores
	NeverPlayed int         // names with no score anywhere
	Errors      []sheetErr  // problems that stop the import
}

// sheetErr is a problem with the sheet; Key is an i18n key under "import.err.".
type sheetErr struct {
	Key  string
	Args []any
}

func normName(s string) string { return strings.Join(strings.Fields(s), " ") }

// sheetNumber reads a numeric cell; ok is false for text.
func sheetNumber(v string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func isNoScore(v string) bool {
	switch strings.TrimSpace(v) {
	case "", "-", "–", "—":
		return true
	}
	return false
}

var sheetMonths = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March, "apr": time.April,
	"may": time.May, "mei": time.May, "jun": time.June, "jul": time.July,
	"aug": time.August, "agu": time.August, "agt": time.August, "sep": time.September,
	"oct": time.October, "okt": time.October, "nov": time.November, "dec": time.December, "des": time.December,
}

// parseSheetDate reads a block's date cell: "Sun, 4 Oct", "4 Okt 2026",
// "2026-10-04", or an Excel date serial. year is used when the cell has none.
func parseSheetDate(v string, year int) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if d, ok := parseDate(v); ok {
		return d, true
	}
	if f, ok := sheetNumber(v); ok && f > 20000 && f < 80000 && f == math.Trunc(f) {
		return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(f)), true
	}
	day, month := 0, time.Month(0)
	for _, tok := range strings.FieldsFunc(strings.ToLower(v), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '/' || r == '-'
	}) {
		if n, err := strconv.Atoi(tok); err == nil {
			switch {
			case n >= 1 && n <= 31 && day == 0:
				day = n
			case n >= 2000 && n <= 2100:
				year = n
			}
			continue
		}
		if len(tok) >= 3 {
			if m, ok := sheetMonths[tok[:3]]; ok {
				month = m
			}
		}
	}
	if day == 0 || month == 0 || year == 0 {
		return time.Time{}, false
	}
	d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return d, d.Day() == day // rejects 31 Nov
}

// parsePointsSheet reads the sheet's play day blocks. target caps scores
// (the club plays to 42; a deuce written as 43 imports as 42), defaultYear is
// used when the sheet has no year cell.
func parsePointsSheet(g *sheetGrid, target, defaultYear int) *sheetParse {
	out := &sheetParse{}
	fail := func(key string, args ...any) { out.Errors = append(out.Errors, sheetErr{key, args}) }

	// The slot header row is the first one with a SubTot cell.
	hdr := 0
	for r := 1; r <= g.Rows && hdr == 0; r++ {
		for c := 1; c <= g.Cols; c++ {
			if isSubTot(g.Cell(r, c)) {
				hdr = r
				break
			}
		}
	}
	if hdr < 2 {
		fail("no_blocks")
		return out
	}
	year := defaultYear
	for r := 1; r < hdr-1 && year == defaultYear; r++ {
		for c := 1; c <= g.Cols; c++ {
			if f, ok := sheetNumber(g.Cell(r, c)); ok && f >= 2000 && f <= 2100 && f == math.Trunc(f) {
				year = int(f)
				break
			}
		}
	}

	type block struct {
		first, n, sub, total int // columns; total is 0 when there's no Total column
		date                 time.Time
		header               string
	}
	var blocks []block
	var prev time.Time
	for c := 1; c <= g.Cols; c++ {
		if !isSubTot(g.Cell(hdr, c)) {
			continue
		}
		// The slot columns right before SubTot read 1, 2, … n.
		ref := colName(c) + strconv.Itoa(hdr)
		last, ok := sheetNumber(g.Cell(hdr, c-1))
		n := int(last)
		if !ok || last != math.Trunc(last) || n < 1 || n >= c {
			fail("slots", ref)
			continue
		}
		first := c - n
		for i := 0; i < n && ok; i++ {
			f, isNum := sheetNumber(g.Cell(hdr, first+i))
			ok = isNum && f == float64(i+1)
		}
		if !ok {
			fail("slots", ref)
			continue
		}
		if n > maxPlaysPerDay {
			fail("too_many_slots", ref, n, maxPlaysPerDay)
			continue
		}
		header := g.Cell(hdr-1, first)
		d, ok := parseSheetDate(header, year)
		if !ok {
			fail("date", colName(first)+strconv.Itoa(hdr-1), header)
			continue
		}
		// A sheet running Dec → Jan without a year on each header.
		for !prev.IsZero() && d.Before(prev.AddDate(0, -6, 0)) {
			d = d.AddDate(1, 0, 0)
		}
		prev = d
		b := block{first: first, n: n, sub: c, date: d, header: header}
		if strings.EqualFold(strings.TrimSpace(g.Cell(hdr, c+1)), "total") {
			b.total = c + 1
		}
		blocks = append(blocks, b)
	}
	if len(blocks) == 0 && len(out.Errors) == 0 {
		fail("no_blocks")
	}

	seenDate := map[time.Time]string{}
	for _, b := range blocks {
		if other, dup := seenDate[b.date]; dup {
			fail("dup_date", other, b.header)
		}
		seenDate[b.date] = b.header
	}

	seenName := map[string]int{}
	type prow struct {
		r    int
		name string
	}
	var rows []prow
	for r := hdr + 1; r <= g.Rows; r++ {
		name := normName(g.Cell(r, 1))
		if name == "" {
			continue
		}
		if tooLong(name, maxPlayerNameLen) {
			fail("name_long", "A"+strconv.Itoa(r), name, maxPlayerNameLen)
			continue
		}
		key := strings.ToLower(name)
		if first, dup := seenName[key]; dup {
			fail("dup_name", "A"+strconv.Itoa(first), "A"+strconv.Itoa(r), name)
			continue
		}
		seenName[key] = r
		rows = append(rows, prow{r, name})
	}

	played := map[string]bool{}
	prevTotal := map[string]float64{}
	for _, b := range blocks {
		day := sheetDay{Date: b.date, Header: b.header}
		for _, p := range rows {
			var pts []int
			for i := 0; i < b.n; i++ {
				ref := colName(b.first+i) + strconv.Itoa(p.r)
				v := g.Cell(p.r, b.first+i)
				if isNoScore(v) {
					continue
				}
				f, ok := sheetNumber(v)
				switch {
				case !ok || f != math.Trunc(f):
					fail("cell", ref, v)
					continue
				case f == -1:
					out.Ignored = append(out.Ignored, sheetNote{Cell: ref, Player: p.name, Date: b.date})
					continue
				case f < 0 || f > maxAwardPoints:
					fail("cell", ref, v)
					continue
				}
				n := int(f)
				if n > target {
					out.Capped = append(out.Capped, sheetNote{Cell: ref, Player: p.name, Date: b.date, A: n, B: target})
					n = target
				}
				pts = append(pts, n)
			}
			subRef := colName(b.sub) + strconv.Itoa(p.r)
			sub, hasSub := sheetNumber(g.Cell(p.r, b.sub))
			if len(pts) > 0 {
				played[p.name] = true
				day.Players = append(day.Players, sheetPlays{Name: p.name, Points: pts})
				sum := 0
				for _, n := range pts {
					sum += n
				}
				if hasSub && float64(sum) != sub {
					out.SubTotal = append(out.SubTotal, sheetNote{Cell: subRef, Player: p.name, Date: b.date, A: int(sub), B: sum})
				}
			}
			// The sheet's own running total: previous Total + this SubTot.
			if b.total > 0 {
				if tot, ok := sheetNumber(g.Cell(p.r, b.total)); ok {
					want := prevTotal[p.name]
					if hasSub {
						want += sub
					}
					if tot != want {
						out.Total = append(out.Total, sheetNote{Cell: colName(b.total) + strconv.Itoa(p.r), Player: p.name, Date: b.date, A: int(tot), B: int(want)})
					}
					prevTotal[p.name] = tot
				} else if hasSub {
					prevTotal[p.name] += sub
				}
			}
		}
		if len(day.Players) == 0 {
			out.EmptyDays = append(out.EmptyDays, b.header)
			continue
		}
		out.Days = append(out.Days, day)
	}
	out.NeverPlayed = len(rows) - len(played)
	return out
}

func isSubTot(v string) bool {
	v = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
	return v == "subtot" || v == "subtotal"
}
