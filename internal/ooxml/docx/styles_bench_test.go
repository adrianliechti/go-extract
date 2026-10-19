package docx

import (
	"strconv"
	"testing"
)

// A repeated six-level style chain exercises the work done for every run in
// a styled Word document. The expectation also prevents losing toggle order.
func BenchmarkInheritedRunFormatting(b *testing.B) {
	styles := &styleTable{byID: map[string]*style{}}
	for i := 0; i < 6; i++ {
		styles.byID[strconv.Itoa(i)] = &style{ID: strconv.Itoa(i), Type: "paragraph", BasedOn: strconv.Itoa(i - 1), RPr: &runProps{Bold: &onOff{}}}
	}
	b.ReportAllocs()
	for b.Loop() {
		got := styles.runFormatting("5", &runProps{Italic: &onOff{}})
		if got.Bold.on() || !got.Italic.on() {
			b.Fatal("style toggle order changed")
		}
	}
}
