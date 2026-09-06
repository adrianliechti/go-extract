package pdf_test

// Differential harness against the Rust reference implementation.
//
// testdata/golden/markdown holds the reference output for every fixture,
// produced by scripts/gen-golden.sh. Comparison uses the same normalisation as
// the reference's own snapshot test (tests/integration_tests.rs assert_snapshot):
// both sides are trailing-whitespace trimmed before comparison.
//
// While the pipeline is being ported this reports a fidelity score rather than
// failing outright, so progress is visible per fixture. Set GOLDEN_STRICT=1 to
// turn any mismatch into a failure.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/adrianliechti/go-extract/internal/pdf"
)

const goldenDir = "testdata/golden/markdown"

// fixturePasswords maps fixtures that need a password to open.
var fixturePasswords = map[string]string{
	"encrypted-secret123": "secret123",
}

func TestGoldenMarkdown(t *testing.T) {
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Skipf("goldens unavailable (run scripts/gen-golden.sh): %v", err)
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, strings.TrimSuffix(e.Name(), ".md"))
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no goldens found")
	}

	strict := os.Getenv("GOLDEN_STRICT") != ""

	var matched, mismatched, unimplemented int
	var report []string

	for _, name := range names {
		want, err := os.ReadFile(filepath.Join(goldenDir, name+".md"))
		if err != nil {
			t.Errorf("%s: read golden: %v", name, err)
			continue
		}

		got, err := pdf.ProcessFile(
			filepath.Join("testdata/fixtures", name+".pdf"),
			pdf.Options{Password: fixturePasswords[name]},
		)
		switch {
		case errors.Is(err, pdf.ErrNotImplemented):
			unimplemented++
			continue
		case err != nil:
			mismatched++
			report = append(report, fmt.Sprintf("  %-46s ERROR %v", name, err))
			if strict {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}

		if trimEnd(got.Markdown) == trimEnd(string(want)) {
			matched++
			continue
		}

		mismatched++
		report = append(report, fmt.Sprintf("  %-46s %s", name, summarise(trimEnd(string(want)), trimEnd(got.Markdown))))
		if strict {
			t.Errorf("%s: markdown mismatch\n%s", name,
				firstDiff(trimEnd(string(want)), trimEnd(got.Markdown)))
		}
	}

	total := len(names)
	if unimplemented == total {
		t.Skipf("pipeline not implemented yet (%d fixtures pending)", total)
	}

	t.Logf("fidelity: %d/%d matched, %d mismatched, %d unimplemented",
		matched, total, mismatched, unimplemented)
	for _, line := range report {
		t.Log(line)
	}
}

// trimEnd mirrors the reference test's str::trim_end.
func trimEnd(s string) string { return strings.TrimRight(s, " \t\r\n") }

func summarise(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	diff := 0
	for i := 0; i < len(wl) || i < len(gl); i++ {
		if lineAt(wl, i) != lineAt(gl, i) {
			diff++
		}
	}
	return fmt.Sprintf("%d/%d lines differ", diff, max(len(wl), len(gl)))
}

// firstDiff renders the first few differing lines, in the reference test's style.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(wl) || i < len(gl); i++ {
		w, g := lineAt(wl, i), lineAt(gl, i)
		if w == g {
			continue
		}
		fmt.Fprintf(&b, "  line %d:\n    want %.100q\n    got  %.100q\n", i+1, w, g)
		if shown++; shown >= 5 {
			b.WriteString("  ... (more differences truncated)\n")
			break
		}
	}
	return b.String()
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<missing>"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
