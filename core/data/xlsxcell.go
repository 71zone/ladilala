package data

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

type cellKind int

const (
	kindOther cellKind = iota
	kindDate
	kindDateTime
	kindTime
)

// classifyFormat decides what a number format shows. It returns the kind and
// whether the format shows seconds.
func classifyFormat(numFmt int, custom string) (cellKind, bool) {
	switch {
	case numFmt == 14 || (numFmt >= 15 && numFmt <= 17) || (numFmt >= 27 && numFmt <= 31) || numFmt == 36 || (numFmt >= 50 && numFmt <= 58):
		return kindDate, false
	case numFmt == 18 || numFmt == 20:
		return kindTime, false
	case numFmt == 19 || numFmt == 21:
		return kindTime, true
	case numFmt == 22:
		return kindDateTime, false
	}
	if custom == "" {
		return kindOther, false
	}
	return classifyCustom(custom)
}

// classifyCustom looks for date and time tokens outside quotes, escapes and
// bracket parts. An elapsed time such as [h]:mm is not a clock time.
func classifyCustom(code string) (cellKind, bool) {
	if i := strings.IndexByte(code, ';'); i >= 0 {
		code = code[:i]
	}
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case inQuote:
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == '[':
			j := strings.IndexByte(code[i:], ']')
			if j < 0 {
				return kindOther, false
			}
			inner := strings.ToLower(code[i+1 : i+j])
			if inner == "h" || inner == "hh" || inner == "m" || inner == "mm" || inner == "s" || inner == "ss" {
				return kindOther, false
			}
			i += j
		default:
			b.WriteByte(c)
		}
	}
	l := strings.ToLower(b.String())
	if strings.Contains(l, "general") {
		return kindOther, false
	}
	hasH := strings.ContainsAny(l, "hs") || strings.Contains(l, "am/pm") || strings.Contains(l, "a/p")
	hasSec := strings.Contains(l, "s")
	hasDate := strings.ContainsAny(l, "yd")
	if strings.Contains(l, "m") && !hasH && !hasDate {
		hasDate = true
	}
	switch {
	case hasDate && hasH:
		return kindDateTime, hasSec
	case hasDate:
		return kindDate, false
	case hasH:
		return kindTime, hasSec
	}
	return kindOther, false
}

type styleInfo struct {
	kind    cellKind
	seconds bool
}

func (s *XLSXSource) kindOf(styleID int) styleInfo {
	s.styleMu.Lock()
	defer s.styleMu.Unlock()
	if k, ok := s.styleKnd[styleID]; ok {
		return styleInfo{kind: k & 0xff, seconds: k&0x100 != 0}
	}
	var info styleInfo
	if st, err := s.f.GetStyle(styleID); err == nil && st != nil {
		custom := ""
		if st.CustomNumFmt != nil {
			custom = *st.CustomNumFmt
		}
		info.kind, info.seconds = classifyFormat(st.NumFmt, custom)
	}
	v := info.kind
	if info.seconds {
		v |= 0x100
	}
	s.styleKnd[styleID] = v
	return info
}

// fixCells rewrites date, time and formula cells of one streamed row in place.
func (s *XLSXSource) fixCells(it *xlsxIter, cells []string) {
	for i := range cells {
		cell, err := excelize.CoordinatesToCellName(i+1, it.rowNum)
		if err != nil {
			return
		}
		if cells[i] == "" {
			s.fixFormula(it, cells, i, cell)
			continue
		}
		styleID, err := s.f.GetCellStyle(s.sheet, cell)
		if err != nil || styleID == 0 {
			continue
		}
		info := s.kindOf(styleID)
		if info.kind == kindOther || (info.kind == kindDate && s.dateFmt == DateDisplay) {
			continue
		}
		raw, err := s.f.GetCellValue(s.sheet, cell, excelize.Options{RawCellValue: true})
		if err != nil {
			continue
		}
		serial, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			// Some tools write a time as text such as 1899-12-30T09:15:00Z.
			if t, perr := time.Parse(time.RFC3339, strings.TrimSpace(raw)); perr == nil && info.kind == kindTime {
				serial = float64(t.Hour()*3600+t.Minute()*60+t.Second()) / 86400
			} else {
				continue
			}
		}
		if serial < 0 {
			continue
		}
		if txt, ok := s.formatSerial(serial, info); ok {
			cells[i] = txt
		}
	}
}

func (s *XLSXSource) formatSerial(serial float64, info styleInfo) (string, bool) {
	switch info.kind {
	case kindTime:
		secs := int(serial*86400 + 0.5)
		secs %= 86400
		h, m, sec := secs/3600, secs%3600/60, secs%60
		if info.seconds {
			return fmt.Sprintf("%02d:%02d:%02d", h, m, sec), true
		}
		return fmt.Sprintf("%02d:%02d", h, m), true
	case kindDate:
		t, err := excelize.ExcelDateToTime(serial, s.date1904)
		if err != nil {
			return "", false
		}
		return t.Format("2006-01-02"), true
	case kindDateTime:
		// Round to the second first so 14:29:59.9999 does not show as 14:29.
		t, err := excelize.ExcelDateToTime(serial, s.date1904)
		if err != nil {
			return "", false
		}
		t = t.Round(time.Second)
		if info.seconds || t.Second() != 0 {
			return t.Format("2006-01-02T15:04:05"), true
		}
		return t.Format("2006-01-02T15:04"), true
	}
	return "", false
}

// fixFormula fills an empty cell that holds a formula without a cached value.
func (s *XLSXSource) fixFormula(it *xlsxIter, cells []string, i int, cell string) {
	formula, err := s.f.GetCellFormula(s.sheet, cell)
	if err != nil || formula == "" {
		return
	}
	val, err := s.f.CalcCellValue(s.sheet, cell)
	if err != nil {
		col := ""
		if i < len(it.header) {
			col = it.header[i]
		}
		s.warnMu.Lock()
		s.warnings = append(s.warnings, FormulaWarning{Row: it.index, Column: col, Cell: cell, Formula: formula, Err: err})
		s.warnMu.Unlock()
		return
	}
	cells[i] = val
}
