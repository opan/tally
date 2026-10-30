#!/usr/bin/env python3
"""Writes scripts/guide-sheet.xlsx, the sample points sheet the guide's import
screenshot uploads. Same fictional club as guide-shots.cjs; laid out like a
club sheet: year in B2, per play day slots 1-7 + SubTot + Total + Rank, with
one deuce (43 and the -1 correction) and one new name. Standard library only.

    python3 scripts/guide-sheet.py
"""
import os
import zipfile
from xml.sax.saxutils import escape

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "guide-sheet.xlsx")


def col(n):
    s = ""
    while n:
        n, r = divmod(n - 1, 26)
        s = chr(65 + r) + s
    return s


cells = {"A1": "BADMINTON SAMPAI PINGSAN", "B2": 2026, "B3": "NOV"}


def block(first, date, label):
    cells[f"{col(first)}4"] = date
    cells[f"{col(first + 1)}4"] = label
    for i in range(7):
        cells[f"{col(first + i)}5"] = i + 1
    cells[f"{col(first + 7)}5"] = "SubTot"
    cells[f"{col(first + 8)}5"] = "Total"
    cells[f"{col(first + 9)}5"] = "Rank"


block(2, "Sun, 1 Nov", "W1")
block(12, "Sun, 8 Nov", "W2")

rows = [
    # name, plays on 1 Nov, plays on 8 Nov
    ("Jacque Oui", [42, 43, 38, -1], [42, 40, 42]),
    ("Prabski", [41, 42, 42], [35, 42]),
    ("Bahlul", [30, 42, 27], [42, 42, 31]),
    ("Mas Gembul", [42, 36], [28, 42, 42]),
    ("Tante Smash", [38, 42, 42], [42, 36]),
    ("Kak Lob Tinggi", [42, 25], [40, 42]),
]
r = 6
for name, d1, d2 in rows:
    cells[f"A{r}"] = name
    total = 0
    for first, plays in ((2, d1), (12, d2)):
        for i in range(7):
            cells[f"{col(first + i)}{r}"] = plays[i] if i < len(plays) else "-"
        sub = sum(plays)  # like the club's sheet, the -1 is in the SubTot
        total += sub
        cells[f"{col(first + 7)}{r}"] = sub
        cells[f"{col(first + 8)}{r}"] = total
    r += 1

shared, index = [], {}
by_row = {}
for ref, v in cells.items():
    n = len(ref.rstrip("0123456789"))
    by_row.setdefault(int(ref[n:]), []).append((ref, v))


def sort_key(ref):
    letters = ref.rstrip("0123456789")
    n = 0
    for ch in letters:
        n = n * 26 + ord(ch) - 64
    return n


sheet = ['<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>']
for row in sorted(by_row):
    sheet.append(f'<row r="{row}">')
    for ref, v in sorted(by_row[row], key=lambda c: sort_key(c[0])):
        if isinstance(v, int):
            sheet.append(f'<c r="{ref}"><v>{v}</v></c>')
        else:
            if v not in index:
                index[v] = len(shared)
                shared.append(v)
            sheet.append(f'<c r="{ref}" t="s"><v>{index[v]}</v></c>')
    sheet.append("</row>")
sheet.append("</sheetData></worksheet>")
sst = '<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">' + \
    "".join(f"<si><t>{escape(s)}</t></si>" for s in shared) + "</sst>"

parts = {
    "[Content_Types].xml": '<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
    '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
    '<Default Extension="xml" ContentType="application/xml"/>'
    '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>'
    '<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
    '<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/></Types>',
    "_rels/.rels": '<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
    '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
    "xl/workbook.xml": '<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" '
    'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Poin" sheetId="1" r:id="rId1"/></sheets></workbook>',
    "xl/_rels/workbook.xml.rels": '<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
    '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>'
    '<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>',
    "xl/worksheets/sheet1.xml": "".join(sheet),
    "xl/sharedStrings.xml": sst,
}
with zipfile.ZipFile(OUT, "w", zipfile.ZIP_DEFLATED) as z:
    for name, body in parts.items():
        z.writestr(zipfile.ZipInfo(name, (2026, 1, 1, 0, 0, 0)), body)
print("wrote", OUT)
