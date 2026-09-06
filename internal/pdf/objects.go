package pdf

import (
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// Accessors that resolve an object through any indirect references before
// reading it. Every PDF structure this package touches may store its members
// either inline or behind a reference, so each read goes through one of these
// rather than a bare type assertion.

func deref(xref *model.XRefTable, o types.Object) types.Object {
	if o == nil {
		return nil
	}
	r, err := xref.Dereference(o)
	if err != nil {
		return nil
	}
	return r
}

func nameOf(xref *model.XRefTable, o types.Object) string {
	if n, ok := deref(xref, o).(types.Name); ok {
		return n.Value()
	}
	return ""
}

func intOf(xref *model.XRefTable, o types.Object) (int64, bool) {
	switch v := deref(xref, o).(type) {
	case types.Integer:
		return int64(v), true
	case types.Float:
		return int64(v), true
	}
	return 0, false
}

func floatOf(xref *model.XRefTable, o types.Object) (float32, bool) {
	switch v := deref(xref, o).(type) {
	case types.Integer:
		return float32(v), true
	case types.Float:
		return float32(v), true
	}
	return 0, false
}

func arrayOf(xref *model.XRefTable, o types.Object) types.Array {
	if a, ok := deref(xref, o).(types.Array); ok {
		return a
	}
	return nil
}

// dictOf returns the dictionary for o, reaching into a stream dictionary when
// the object is a stream (font descriptors and CMaps are often stored that way).
func dictOf(xref *model.XRefTable, o types.Object) types.Dict {
	switch v := deref(xref, o).(type) {
	case types.Dict:
		return v
	case types.StreamDict:
		return v.Dict
	}
	return nil
}

// streamBytesOf returns the decoded contents of a stream object.
func streamBytesOf(xref *model.XRefTable, o types.Object) []byte {
	sd, ok := deref(xref, o).(types.StreamDict)
	if !ok {
		return nil
	}
	if err := sd.Decode(); err != nil {
		return nil
	}
	return sd.Content
}

// boolOf reads a boolean, reporting whether one was present.
func boolOf(xref *model.XRefTable, o types.Object) (bool, bool) {
	if b, ok := deref(xref, o).(types.Boolean); ok {
		return bool(b), true
	}
	return false, false
}
