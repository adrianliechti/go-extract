package xlsx

import (
	"reflect"
	"testing"

	"github.com/adrianliechti/go-extract/internal/ooxml/opc"
)

func TestResolveHyperlinks(t *testing.T) {
	rels := opc.Relationships{
		"rExternal": {
			ID:       "rExternal",
			Type:     opc.RelHyperlink,
			Target:   "https://example.com/book",
			External: true,
		},
		"rImage": {
			ID:       "rImage",
			Type:     opc.RelImage,
			Target:   "https://example.com/not-a-link",
			External: true,
		},
	}
	got := resolveHyperlinks(rels, []xmlHyperlink{
		{Ref: "A1", ID: "rExternal", Display: "External"},
		{Ref: "$B$2", Location: "'Other Sheet'!A1", Display: "Internal"},
		{Ref: "C3:D4", ID: "rExternal", Location: "Sheet1!A1"},
		{Ref: "D4", ID: "rImage"},
		{Ref: "invalid", Location: "Sheet1!A1"},
	})
	want := map[cellCoord]sheetHyperlink{
		{row: 1, col: 1}: {target: "https://example.com/book", display: "External"},
		{row: 2, col: 2}: {target: "#'Other Sheet'!A1", display: "Internal"},
		{row: 3, col: 3}: {target: "https://example.com/book#Sheet1!A1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveHyperlinks = %#v, want %#v", got, want)
	}
}
