package xlsx

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Number formats decide how a stored cell value is displayed. The value itself
// is always a plain number, so a date is indistinguishable from a quantity
// without consulting its format.
//
// The policy here is deliberately conservative: convert only what has a
// well-defined textual meaning — dates, times and percentages — and otherwise
// emit the stored value unchanged. In particular, General is NOT treated as a
// decimal format: a cell storing 89 renders as "89", not "89.0". Padding
// integers with a fractional zero is a property of a consumer that parses
// cells into floats, not of the spreadsheet.

// builtinDateFormats are the reserved numFmtIds that denote a date or time
// (ECMA-376 §18.8.30). Ids outside this set are either non-temporal builtins
// or custom formats defined in styles.xml.
//
// A reserved id carries no formatCode in styles.xml, so an unrecognised one is
// invisible: its cells would render as raw serial numbers. That makes the
// East-Asian block below matter — Office pre-assigns those ids to files
// authored in a Japanese locale, and the spec lists them without format
// strings. The codes here mirror what Excel ja-JP writes back on re-save.
//
// Only the date/time classification is used; values are rendered as ISO 8601
// regardless of the locale the format came from, so extracted Markdown stays
// unambiguous.
var builtinDateFormats = map[int]string{
	// US-English built-ins.
	14: "m/d/yy",
	15: "d-mmm-yy",
	16: "d-mmm",
	17: "mmm-yy",
	18: "h:mm AM/PM",
	19: "h:mm:ss AM/PM",
	20: "h:mm",
	21: "h:mm:ss",
	22: "m/d/yy h:mm",
	45: "mm:ss",
	46: "[h]:mm:ss",
	47: "mmss.0",

	// East-Asian (Japanese) locale built-ins.
	27: "[$-411]ge.m.d",
	28: `[$-411]ggge"年"m"月"d"日"`,
	29: `[$-411]ggge"年"m"月"d"日"`,
	30: "m/d/yy",
	31: `yyyy"年"m"月"d"日"`,
	50: "[$-411]ge.m.d",
	51: `[$-411]ggge"年"m"月"d"日"`,
	52: `yyyy"年"m"月"`,
	53: `m"月"d"日"`,
	54: `[$-411]ggge"年"m"月"d"日"`,
	55: `yyyy"年"m"月"`,
	56: `m"月"d"日"`,
	57: "[$-411]ge.m.d",
	58: `[$-411]ggge"年"m"月"d"日"`,
}

// builtinPercent are the reserved percentage formats.
var builtinPercent = map[int]bool{9: true, 10: true}

// numFormat describes what a cell's format means for rendering.
type numFormat struct {
	isDate    bool
	hasDate   bool
	hasTime   bool
	isPercent bool
	decimals  int // digits after the point for percentages
}

// classifyFormat interprets a format code. An unrecognised or literal-heavy
// code yields the zero value, which means "emit the stored value unchanged".
func classifyFormat(id int, code string) numFormat {
	var f numFormat

	if builtinPercent[id] {
		f.isPercent = true
		if id == 10 {
			f.decimals = 2
		}
		return f
	}

	if code == "" {
		if builtin, ok := builtinDateFormats[id]; ok {
			code = builtin
		} else {
			return f
		}
	}

	// A code may carry several sections separated by ';' (positive; negative;
	// zero; text). Only the first governs ordinary values.
	if i := strings.IndexByte(code, ';'); i >= 0 {
		code = code[:i]
	}

	hasDate, hasTime, literalOnly := scanFormatTokens(code)
	if literalOnly {
		// Codes such as LibreOffice's `\1\.0#` are entirely escaped literals
		// and placeholders. Applying them would corrupt plain numbers, so the
		// value passes through untouched.
		return f
	}

	if hasDate || hasTime {
		f.isDate = true
		f.hasDate = hasDate
		f.hasTime = hasTime
		return f
	}

	if strings.Contains(code, "%") {
		f.isPercent = true
		f.decimals = decimalsIn(code)
	}
	return f
}

// scanFormatTokens walks a format code, ignoring quoted literals, escaped
// characters and bracketed sections, and reports whether live date or time
// tokens remain.
func scanFormatTokens(code string) (hasDate, hasTime, literalOnly bool) {
	sawLive := false

	for i := 0; i < len(code); i++ {
		switch c := code[i]; c {
		case '\\':
			i++ // the next character is a literal
			continue
		case '"':
			// Skip to the closing quote.
			for i++; i < len(code) && code[i] != '"'; i++ {
			}
			continue
		case '[':
			// Bracketed sections hold colours and conditions. [h], [m] and [s]
			// are elapsed-time tokens and do count.
			end := strings.IndexByte(code[i:], ']')
			if end < 0 {
				i = len(code)
				continue
			}
			inner := strings.ToLower(code[i+1 : i+end])
			if inner == "h" || inner == "hh" || inner == "m" || inner == "mm" || inner == "s" || inner == "ss" {
				hasTime, sawLive = true, true
			}
			i += end
			continue
		}

		switch c := lower(code[i]); c {
		case 'y', 'd':
			hasDate, sawLive = true, true
		case 'h', 's':
			hasTime, sawLive = true, true
		case 'm':
			// 'm' is minutes next to an hour or second token, otherwise month.
			if adjacentToTime(code, i) {
				hasTime = true
			} else {
				hasDate = true
			}
			sawLive = true
		case '0', '#', '?', '%', 'e':
			sawLive = true
		}
	}
	return hasDate, hasTime, !sawLive
}

// adjacentToTime reports whether an 'm' run is bounded by an hour or second
// token, which makes it minutes rather than months.
func adjacentToTime(code string, i int) bool {
	// Walk back over the current run of 'm'.
	start := i
	for start > 0 && lower(code[start-1]) == 'm' {
		start--
	}
	end := i
	for end+1 < len(code) && lower(code[end+1]) == 'm' {
		end++
	}

	for j := start - 1; j >= 0; j-- {
		c := lower(code[j])
		if c == 'h' {
			return true
		}
		if c == ']' || c == ':' || c == ' ' {
			continue
		}
		break
	}
	for j := end + 1; j < len(code); j++ {
		c := lower(code[j])
		if c == 's' {
			return true
		}
		if c == ':' || c == ' ' || c == '.' {
			continue
		}
		break
	}
	return false
}

func lower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + 32
	}
	return b
}

// decimalsIn counts the digit placeholders after the decimal point.
func decimalsIn(code string) int {
	dot := strings.IndexByte(code, '.')
	if dot < 0 {
		return 0
	}
	n := 0
	for i := dot + 1; i < len(code); i++ {
		if code[i] == '0' || code[i] == '#' {
			n++
			continue
		}
		break
	}
	return n
}

// ── value rendering ──────────────────────────────────────────────────

// render applies a format to a stored numeric value. It returns the original
// text unchanged whenever the format carries no textual meaning.
func (f numFormat) render(raw string, date1904 bool) string {
	if !f.isDate && !f.isPercent {
		return denoise(raw)
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return raw
	}

	if f.isPercent {
		return strconv.FormatFloat(v*100, 'f', f.decimals, 64) + "%"
	}

	t, ok := serialToTime(v, date1904)
	if !ok {
		return raw
	}
	switch {
	case f.hasDate && f.hasTime:
		return t.Format("2006-01-02 15:04:05")
	case f.hasTime:
		return t.Format("15:04:05")
	default:
		return t.Format("2006-01-02")
	}
}

// serialToTime converts an Excel date serial to a time.
//
// The 1900 system has a deliberate off-by-one: Excel treats 1900 as a leap
// year for Lotus compatibility, so serial 60 denotes a 29 February that never
// existed. Serials at or beyond 61 are therefore one day ahead of the true
// count, which the shifted epoch absorbs.
func serialToTime(serial float64, date1904 bool) (time.Time, bool) {
	if math.IsNaN(serial) || math.IsInf(serial, 0) || serial < 0 || serial > 2958466 {
		return time.Time{}, false
	}

	var epoch time.Time
	switch {
	case date1904:
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case serial >= 61:
		epoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	default:
		epoch = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	}

	days := math.Floor(serial)
	frac := serial - days
	// Round to the nearest second; spreadsheet times are stored as binary
	// fractions and land a hair short of the intended value.
	secs := math.Round(frac * 86400)

	return epoch.AddDate(0, 0, int(days)).Add(time.Duration(secs) * time.Second), true
}

// noisyDecimals is the number of fractional digits beyond which a stored value
// is treated as float-serialisation noise rather than intent. Spreadsheets
// round-trip binary doubles through decimal text, so a value entered as 702.6
// can be written as 702.5999999999999.
const noisyDecimals = 12

// denoise collapses float-serialisation artefacts in a stored numeric value.
//
// Excel itself displays at most 15 significant digits, which is what hides the
// artefact in the application. The same rule is applied here, but only to
// values whose fractional part is long enough to be noise — a deliberately
// precise value with fewer digits passes through untouched.
func denoise(raw string) string {
	dot := strings.IndexByte(raw, '.')
	if dot < 0 {
		return raw
	}
	// Count fractional digits; bail out on exponents or trailing junk, where
	// the stored text is not a plain decimal.
	frac := 0
	for i := dot + 1; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return raw
		}
		frac++
	}
	if frac < noisyDecimals {
		return raw
	}

	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw
	}
	// 'g' with precision 15 matches Excel's display precision; the result is
	// then trimmed so 702.600000000000 reads as 702.6.
	s := strconv.FormatFloat(v, 'g', 15, 64)
	if strings.ContainsAny(s, "eE") {
		// Exponent form would be less readable than the original text.
		return raw
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "" || s == "-" {
		return raw
	}
	return s
}
