package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
)

// A minimal reader for the first worksheet of an .xlsx file (Office Open
// XML), enough to import a points sheet: cell values as text, no styles or
// formulas (a formula cell gives its cached value). Only the standard library:
// archive/zip and encoding/xml, which does not fetch external entities.

const (
	maxXLSXPart = 8 << 20 // uncompressed bytes read from any one part (zip bomb guard)
	maxXLSXRows = 2000
	maxXLSXCols = 400
)

var errNotXLSX = errors.New("not an xlsx workbook")

// sheetGrid is a worksheet as rows of cell text; Cell is "" outside it.
type sheetGrid struct {
	Name  string
	Cells map[[2]int]string // [row, col], both 1-based
	Rows  int
	Cols  int
}

func (g *sheetGrid) Cell(row, col int) string { return g.Cells[[2]int{row, col}] }

// colName turns 1 into "A", 28 into "AB".
func colName(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}

// cellRef parses "AB12" into row 12, column 28.
func cellRef(ref string) (row, col int, ok bool) {
	i := 0
	for i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z' {
		col = col*26 + int(ref[i]-'A'+1)
		i++
	}
	if i == 0 || i == len(ref) {
		return 0, 0, false
	}
	row, err := strconv.Atoi(ref[i:])
	return row, col, err == nil && row > 0
}

func readZipPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			data, err := io.ReadAll(io.LimitReader(rc, maxXLSXPart+1))
			if err != nil {
				return nil, err
			}
			if len(data) > maxXLSXPart {
				return nil, fmt.Errorf("%s is too large", name)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("%w: %s missing", errNotXLSX, name)
}

// readFirstSheet returns the first worksheet (in workbook order) of an .xlsx.
func readFirstSheet(data []byte) (*sheetGrid, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errNotXLSX
	}
	wbData, err := readZipPart(zr, "xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wbData, &wb); err != nil || len(wb.Sheets) == 0 {
		return nil, errNotXLSX
	}
	relData, err := readZipPart(zr, "xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	var rels struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(relData, &rels); err != nil {
		return nil, errNotXLSX
	}
	target := ""
	for _, r := range rels.Rels {
		if r.ID == wb.Sheets[0].RID {
			target = r.Target
		}
	}
	if target == "" {
		return nil, errNotXLSX
	}
	// Targets are relative to xl/ ("worksheets/sheet1.xml") or absolute ("/xl/...").
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = path.Join("xl", target)
	}

	var shared []string
	if ssData, err := readZipPart(zr, "xl/sharedStrings.xml"); err == nil {
		var sst struct {
			SI []struct {
				T string `xml:"t"`
				R []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"si"`
		}
		if err := xml.Unmarshal(ssData, &sst); err != nil {
			return nil, errNotXLSX
		}
		for _, si := range sst.SI {
			s := si.T
			for _, r := range si.R { // rich text runs
				s += r.T
			}
			shared = append(shared, s)
		}
	} else if !errors.Is(err, errNotXLSX) {
		return nil, err
	}

	sheetData, err := readZipPart(zr, target)
	if err != nil {
		return nil, err
	}
	var ws struct {
		Rows []struct {
			Cells []struct {
				R  string `xml:"r,attr"`
				T  string `xml:"t,attr"`
				V  string `xml:"v"`
				IS struct {
					T string `xml:"t"`
				} `xml:"is"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := xml.Unmarshal(sheetData, &ws); err != nil {
		return nil, errNotXLSX
	}
	g := &sheetGrid{Name: wb.Sheets[0].Name, Cells: map[[2]int]string{}}
	for _, row := range ws.Rows {
		for _, c := range row.Cells {
			r, col, ok := cellRef(c.R)
			if !ok {
				continue
			}
			if r > maxXLSXRows || col > maxXLSXCols {
				return nil, fmt.Errorf("sheet is larger than %d rows × %d columns", maxXLSXRows, maxXLSXCols)
			}
			v := c.V
			switch c.T {
			case "s":
				i, err := strconv.Atoi(v)
				if err != nil || i < 0 || i >= len(shared) {
					continue
				}
				v = shared[i]
			case "inlineStr":
				v = c.IS.T
			}
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			g.Cells[[2]int{r, col}] = v
			g.Rows, g.Cols = max(g.Rows, r), max(g.Cols, col)
		}
	}
	return g, nil
}
