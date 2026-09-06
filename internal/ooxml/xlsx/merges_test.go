package xlsx

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"testing"
)

func TestMergeCoverageMatchesCellSemantics(t *testing.T) {
	// Compare the sweep against direct rectangle membership on small grids.
	// Overlaps, reversed endpoints, clipped anchors and duplicate ranges all
	// exercise boundaries without relying on any private Office documents.
	rng := rand.New(rand.NewPCG(17, 55))
	for trial := range 100 {
		var ranges []struct{ left, top, right, bottom int }
		var merges []struct {
			Ref string `xml:"ref,attr"`
		}
		for range 12 {
			left, right := rng.IntN(10)+1, rng.IntN(10)+1
			top, bottom := rng.IntN(10)+1, rng.IntN(10)+1
			merges = append(merges, struct {
				Ref string `xml:"ref,attr"`
			}{fmt.Sprintf("%c%d:%c%d", 'A'+left-1, top, 'A'+right-1, bottom)})
			ranges = append(ranges, struct{ left, top, right, bottom int }{
				min(left, right), min(top, bottom), max(left, right), max(top, bottom),
			})
		}
		minCol := rng.IntN(5)
		coverage := newMergeCoverage(merges, minCol, 10)
		for row := 1; row <= 12; row++ {
			coverage.advance(row)
			coverage.advance(row) // duplicate authored row numbers
			for col := minCol + 1; col <= 10; col++ {
				want := false
				for _, r := range ranges {
					if col >= r.left && col <= r.right && row >= r.top && row <= r.bottom && (col != r.left || row != r.top) {
						want = true
					}
				}
				if got := coverage.counts[col-minCol-1] > 0; got != want {
					t.Fatalf("trial %d, cell %d,%d: merged = %v, want %v", trial, col, row, got, want)
				}
			}
		}
	}
}

func TestMergeCoverageHandlesExtremeRowsWithoutOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	merges := []struct {
		Ref string `xml:"ref,attr"`
	}{
		{Ref: "A1:B" + strconv.Itoa(maxInt)},
		{Ref: "D" + strconv.Itoa(maxInt) + ":E" + strconv.Itoa(maxInt)},
		{Ref: "invalid"},
	}
	coverage := newMergeCoverage(merges, 0, 5)
	coverage.advance(1)
	if coverage.counts[0] != 0 || coverage.counts[1] != 1 {
		t.Fatalf("first row coverage = %v; anchor must remain visible", coverage.counts)
	}
	coverage.advance(maxInt)
	for col, want := range []int{1, 1, 0, 0, 1} {
		if coverage.counts[col] != want {
			t.Fatalf("last row coverage = %v", coverage.counts)
		}
	}
}
