package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

// renderOMML parses a bare OMML fragment and returns its LaTeX.
func renderOMML(t *testing.T, fragment string) string {
	t.Helper()
	const ns = `xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"`
	doc := `<m:oMath ` + ns + `>` + fragment + `</m:oMath>`

	dec := xml.NewDecoder(strings.NewReader(doc))
	dec.Strict = false
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	start, ok := tok.(xml.StartElement)
	if !ok {
		t.Fatalf("expected a start element, got %T", tok)
	}
	latex, _ := parseOMML(dec, start)
	return latex
}

func mr(s string) string { return `<m:r><m:t>` + s + `</m:t></m:r>` }

func TestOMMLToLaTeX(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		want     string
	}{
		{
			// The case that motivates this: raw text would give "r2", which
			// reads as a product rather than a power.
			name:     "superscript",
			fragment: `<m:sSup><m:e>` + mr("r") + `</m:e><m:sup>` + mr("2") + `</m:sup></m:sSup>`,
			want:     `r^{2}`,
		},
		{
			name:     "subscript",
			fragment: `<m:sSub><m:e>` + mr("a") + `</m:e><m:sub>` + mr("n") + `</m:sub></m:sSub>`,
			want:     `a_{n}`,
		},
		{
			name: "fraction",
			fragment: `<m:f><m:num>` + mr("x") + `</m:num>` +
				`<m:den>` + mr("y") + `</m:den></m:f>`,
			want: `\frac{x}{y}`,
		},
		{
			name:     "delimiters",
			fragment: `<m:d><m:e>` + mr("x") + `</m:e></m:d>`,
			want:     `\left(x\right)`,
		},
		{
			name:     "radical",
			fragment: `<m:rad><m:deg></m:deg><m:e>` + mr("2") + `</m:e></m:rad>`,
			want:     `\sqrt{2}`,
		},
		{
			name:     "radical with degree",
			fragment: `<m:rad><m:deg>` + mr("3") + `</m:deg><m:e>` + mr("x") + `</m:e></m:rad>`,
			want:     `\sqrt[3]{x}`,
		},
		{
			name: "n-ary summation",
			fragment: `<m:nary><m:naryPr><m:chr m:val="∑"/></m:naryPr>` +
				`<m:sub>` + mr("n=1") + `</m:sub><m:sup>` + mr("10") + `</m:sup>` +
				`<m:e>` + mr("x") + `</m:e></m:nary>`,
			want: `\sum_{n=1}^{10}{x}`,
		},
		{
			// An absent chr defaults to summation per the OMML schema.
			name:     "n-ary default operator",
			fragment: `<m:nary><m:e>` + mr("x") + `</m:e></m:nary>`,
			want:     `\sum{x}`,
		},
		{
			name: "n-ary integral",
			fragment: `<m:nary><m:naryPr><m:chr m:val="∫"/></m:naryPr>` +
				`<m:e>` + mr("f") + `</m:e></m:nary>`,
			want: `\int{f}`,
		},
		{
			// A bare "cos" would typeset as a product of three variables.
			name: "named function",
			fragment: `<m:func><m:fName>` + mr("cos") + `</m:fName>` +
				`<m:e>` + mr("x") + `</m:e></m:func>`,
			want: `\cos{x}`,
		},
		{
			name:     "greek and operators",
			fragment: mr("π") + mr("×") + mr("∞"),
			want:     `\pi \times \infty`,
		},
		{
			// A multi-character base needs braces or the exponent binds only
			// to the last character.
			name:     "grouped base",
			fragment: `<m:sSup><m:e>` + mr("ab") + `</m:e><m:sup>` + mr("2") + `</m:sup></m:sSup>`,
			want:     `{ab}^{2}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(renderOMML(t, tc.fragment))
			// Symbol commands carry a trailing space to avoid running into the
			// next token; collapse runs of spaces before comparing.
			got = strings.Join(strings.Fields(got), " ")
			want := strings.Join(strings.Fields(tc.want), " ")
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestOMMLEquationArrayRows pins the row/term distinction: each m:e in an
// eqArr is a row, so terms within one row must not be split apart.
func TestOMMLEquationArrayRows(t *testing.T) {
	single := `<m:eqArr><m:e>` + mr("a") + mr("+") + mr("b") + `</m:e></m:eqArr>`
	if got := renderOMML(t, single); got != "a+b" {
		t.Errorf("single row = %q, want %q", got, "a+b")
	}

	two := `<m:eqArr><m:e>` + mr("a") + `</m:e><m:e>` + mr("b") + `</m:e></m:eqArr>`
	want := `\begin{aligned}a \\ b\end{aligned}`
	if got := renderOMML(t, two); got != want {
		t.Errorf("two rows = %q, want %q", got, want)
	}
}

// TestOMMLUnknownConstructKeepsText ensures an unsupported element degrades to
// its text rather than dropping content.
func TestOMMLUnknownConstructKeepsText(t *testing.T) {
	frag := `<m:groupChr><m:e>` + mr("xyz") + `</m:e></m:groupChr>`
	if got := renderOMML(t, frag); !strings.Contains(got, "xyz") {
		t.Errorf("got %q, want it to retain %q", got, "xyz")
	}
}

func TestLatexTextEscaping(t *testing.T) {
	tests := []struct{ in, want string }{
		{"x", "x"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{"{x}", `\{x\}`},
		{"$", `\$`},
	}
	for _, tc := range tests {
		if got := latexText(tc.in); got != tc.want {
			t.Errorf("latexText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
