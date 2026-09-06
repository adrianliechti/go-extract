package xlsx

import "testing"

func TestClassifyFormat(t *testing.T) {
	tests := []struct {
		name    string
		id      int
		code    string
		date    bool
		hasDate bool
		hasTime bool
		percent bool
	}{
		// General and Text carry no textual meaning: values pass through.
		{name: "general", id: 0},
		{name: "text", id: 49, code: "@"},

		// LibreOffice writes this escaped-literal placeholder where Excel
		// would write General. Applying it would corrupt plain numbers.
		{name: "escaped literal placeholder", id: 164, code: `\1\.0#`},

		// Plain numeric formats are still pass-through: a stored 89 must not
		// become 89.0 just because the format shows decimals.
		{name: "two decimals", id: 2, code: "0.00"},
		{name: "thousands", id: 3, code: "#,##0"},

		{name: "builtin date", id: 14, date: true, hasDate: true},
		{name: "builtin datetime", id: 22, date: true, hasDate: true, hasTime: true},
		{name: "builtin time", id: 21, date: true, hasTime: true},
		{name: "elapsed hours", id: 46, date: true, hasTime: true},

		{name: "custom iso date", id: 165, code: "yyyy-mm-dd", date: true, hasDate: true},
		{name: "custom datetime", id: 166, code: "yyyy-mm-dd hh:mm:ss", date: true, hasDate: true, hasTime: true},
		{name: "custom time only", id: 167, code: "hh:mm", date: true, hasTime: true},

		// East-Asian locale built-ins. These carry no formatCode in styles.xml,
		// so an unrecognised id would silently render serial numbers.
		{name: "ja imperial era", id: 27, date: true, hasDate: true},
		{name: "ja long era date", id: 28, date: true, hasDate: true},
		{name: "ja short date", id: 30, date: true, hasDate: true},
		{name: "ja year month day", id: 31, date: true, hasDate: true},
		{name: "ja era 50", id: 50, date: true, hasDate: true},
		{name: "ja year month", id: 52, date: true, hasDate: true},
		{name: "ja month day", id: 53, date: true, hasDate: true},
		{name: "ja era 58", id: 58, date: true, hasDate: true},

		{name: "builtin percent", id: 9, percent: true},
		{name: "builtin percent 2dp", id: 10, percent: true},
		{name: "custom percent", id: 168, code: "0.0%", percent: true},

		// A quoted literal containing date letters must not read as a date.
		{name: "quoted literal", id: 169, code: `"day"0`},
		// Escaped date letters likewise.
		{name: "escaped letters", id: 170, code: `\y\y0`},
		// Currency is a plain number.
		{name: "currency", id: 171, code: `"$"#,##0.00`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := classifyFormat(tc.id, tc.code)
			if f.isDate != tc.date {
				t.Errorf("isDate = %v, want %v", f.isDate, tc.date)
			}
			if f.hasDate != tc.hasDate {
				t.Errorf("hasDate = %v, want %v", f.hasDate, tc.hasDate)
			}
			if f.hasTime != tc.hasTime {
				t.Errorf("hasTime = %v, want %v", f.hasTime, tc.hasTime)
			}
			if f.isPercent != tc.percent {
				t.Errorf("isPercent = %v, want %v", f.isPercent, tc.percent)
			}
		})
	}
}

// TestGeneralPassesThrough pins the decision not to reproduce a consumer's
// float coercion: a stored integer renders as an integer.
func TestGeneralPassesThrough(t *testing.T) {
	f := classifyFormat(0, "")
	for _, raw := range []string{"89", "0", "-12", "3.5", "1e6", "not a number"} {
		if got := f.render(raw, false); got != raw {
			t.Errorf("render(%q) = %q, want it unchanged", raw, got)
		}
	}
}

func TestRenderDates(t *testing.T) {
	tests := []struct {
		name     string
		id       int
		code     string
		serial   string
		date1904 bool
		want     string
	}{
		// 45678 is 2025-01-21 in the 1900 system.
		{name: "iso date", id: 165, code: "yyyy-mm-dd", serial: "45678", want: "2025-01-21"},
		{name: "builtin date", id: 14, serial: "45678", want: "2025-01-21"},

		// Serial 1 is 1900-01-01; the leap-year quirk applies only from 61 on.
		{name: "epoch day", id: 14, serial: "1", want: "1900-01-01"},
		{name: "before the phantom day", id: 14, serial: "59", want: "1900-02-28"},
		{name: "after the phantom day", id: 14, serial: "61", want: "1900-03-01"},

		// Half a day past the epoch is noon.
		{name: "datetime", id: 22, serial: "45678.5", want: "2025-01-21 12:00:00"},
		{name: "time only", id: 21, serial: "0.75", want: "18:00:00"},

		// The 1904 system starts four years and a day later.
		{name: "1904 system", id: 14, serial: "44216", date1904: true, want: "2025-01-21"},

		// East-Asian built-ins render as ISO 8601 like every other date, so
		// extracted Markdown stays unambiguous regardless of source locale.
		{name: "ja era renders iso", id: 27, serial: "45678", want: "2025-01-21"},
		{name: "ja long date renders iso", id: 28, serial: "45678", want: "2025-01-21"},
		{name: "ja month day renders iso", id: 53, serial: "45678", want: "2025-01-21"},

		// Values that cannot be a date fall through untouched.
		{name: "not numeric", id: 14, serial: "abc", want: "abc"},
		{name: "negative", id: 14, serial: "-5", want: "-5"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := classifyFormat(tc.id, tc.code)
			if got := f.render(tc.serial, tc.date1904); got != tc.want {
				t.Errorf("render(%q) = %q, want %q", tc.serial, got, tc.want)
			}
		})
	}
}

func TestRenderPercent(t *testing.T) {
	tests := []struct {
		id   int
		code string
		raw  string
		want string
	}{
		{id: 9, raw: "0.5", want: "50%"},
		{id: 10, raw: "0.5", want: "50.00%"},
		{id: 172, code: "0.0%", raw: "0.125", want: "12.5%"},
		{id: 9, raw: "1", want: "100%"},
		{id: 9, raw: "oops", want: "oops"},
	}
	for _, tc := range tests {
		f := classifyFormat(tc.id, tc.code)
		if got := f.render(tc.raw, false); got != tc.want {
			t.Errorf("id=%d code=%q render(%q) = %q, want %q", tc.id, tc.code, tc.raw, got, tc.want)
		}
	}
}

// TestMonthMinuteDisambiguation covers the one genuinely ambiguous token: 'm'
// means minutes beside an hour or second token, and months otherwise.
func TestMonthMinuteDisambiguation(t *testing.T) {
	tests := []struct {
		code    string
		hasDate bool
		hasTime bool
	}{
		{code: "mm/dd/yyyy", hasDate: true},
		{code: "hh:mm", hasTime: true},
		{code: "mm:ss", hasTime: true},
		{code: "h:mm:ss", hasTime: true},
		{code: "yyyy-mm-dd hh:mm", hasDate: true, hasTime: true},
		{code: "mmm", hasDate: true},
	}
	for _, tc := range tests {
		f := classifyFormat(200, tc.code)
		if f.hasDate != tc.hasDate || f.hasTime != tc.hasTime {
			t.Errorf("%q: hasDate=%v hasTime=%v, want hasDate=%v hasTime=%v",
				tc.code, f.hasDate, f.hasTime, tc.hasDate, tc.hasTime)
		}
	}
}
