package xlsx

import "testing"

func TestDenoise(t *testing.T) {
	tests := []struct{ in, want string }{
		{"702.5999999999999", "702.6"}, // classic Excel round-trip artefact
		{"0.1000000000000001", "0.1"},
		{"3.0000000000000004", "3"},
		{"702.6", "702.6"},                 // already clean: untouched
		{"89", "89"},                       // integers untouched
		{"3.14159", "3.14159"},             // deliberate precision untouched
		{"1.23456789012", "1.23456789012"}, // 11 decimals: below the threshold
		{"not a number", "not a number"},
		{"1e10", "1e10"}, // exponent form untouched
		{"", ""},
	}
	for _, tc := range tests {
		if got := denoise(tc.in); got != tc.want {
			t.Errorf("denoise(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
