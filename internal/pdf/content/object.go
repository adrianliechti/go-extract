// Package content tokenizes PDF content streams into operator/operand pairs.
//
// It deliberately uses its own object model rather than pdfcpu's: content
// stream strings carry arbitrary bytes whose interpretation depends on the
// active font's encoding, so they must survive tokenization unmodified.
package content

import "fmt"

// Object is an operand appearing in a content stream.
type Object interface{ object() }

type (
	// Integer is a PDF integer operand.
	Integer int64
	// Real is a PDF real (floating point) operand.
	Real float64
	// Name is a PDF name operand, stored without the leading slash.
	Name string
	// String holds the decoded bytes of a literal `(...)` or hex `<...>`
	// string. Escape sequences are resolved; encoding is not interpreted.
	String []byte
	// Array is a PDF array operand.
	Array []Object
	// Dict is a PDF dictionary operand, used by BDC/DP and inline images.
	Dict map[Name]Object
	// Bool is a PDF boolean operand.
	Bool bool
	// Null is the PDF null operand.
	Null struct{}
)

func (Integer) object() {}
func (Real) object()    {}
func (Name) object()    {}
func (String) object()  {}
func (Array) object()   {}
func (Dict) object()    {}
func (Bool) object()    {}
func (Null) object()    {}

// Operation is a single content stream instruction: an operator preceded by
// the operands it consumes.
type Operation struct {
	Operator string
	Operands []Object
}

func (o Operation) String() string {
	return fmt.Sprintf("%v %s", o.Operands, o.Operator)
}

// Float returns the numeric value of a numeric operand. Both Integer and Real
// are accepted because PDF writers use them interchangeably for coordinates.
func Float(o Object) (float64, bool) {
	switch v := o.(type) {
	case Integer:
		return float64(v), true
	case Real:
		return float64(v), true
	}
	return 0, false
}

// Int returns the integer value of a numeric operand, truncating a Real.
func Int(o Object) (int64, bool) {
	switch v := o.(type) {
	case Integer:
		return int64(v), true
	case Real:
		return int64(v), true
	}
	return 0, false
}
