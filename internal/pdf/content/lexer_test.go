package content

import (
	"bytes"
	"testing"
)

func ops(t *testing.T, src string) []Operation {
	t.Helper()
	got, err := Decode([]byte(src))
	if err != nil {
		t.Fatalf("Decode(%q): %v", src, err)
	}
	return got
}

func TestBasicTextOperators(t *testing.T) {
	got := ops(t, "BT /F1 12 Tf 100 200 Td (Hello) Tj ET")

	want := []string{"BT", "Tf", "Td", "Tj", "ET"}
	if len(got) != len(want) {
		t.Fatalf("got %d ops, want %d: %v", len(got), len(want), got)
	}
	for i, op := range got {
		if op.Operator != want[i] {
			t.Errorf("op %d = %q, want %q", i, op.Operator, want[i])
		}
	}

	if n, ok := got[1].Operands[0].(Name); !ok || n != "F1" {
		t.Errorf("Tf operand 0 = %v, want Name(F1)", got[1].Operands[0])
	}
	if s, ok := got[3].Operands[0].(String); !ok || string(s) != "Hello" {
		t.Errorf("Tj operand = %v, want String(Hello)", got[3].Operands[0])
	}
}

// The Rust extractor needs a strip_pdf_comments pre-pass because lopdf
// mis-parses these. Lexing comments inline handles both correctly.
func TestCommentHandling(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string // expected Tj string operand
	}{
		{"comment between ops", "BT % this is a comment\n(hi) Tj ET", "hi"},
		{"percent inside string", "BT (100% done) Tj ET", "100% done"},
		{"percent inside hex string", "BT <25> Tj ET", "%"},
		{"comment at eof", "BT (x) Tj ET % trailing", "x"},
		{"comment with cr only", "BT % c\r(y) Tj ET", "y"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var found bool
			for _, op := range ops(t, tc.src) {
				if op.Operator == "Tj" && len(op.Operands) == 1 {
					s, ok := op.Operands[0].(String)
					if !ok {
						t.Fatalf("Tj operand not a String: %T", op.Operands[0])
					}
					if string(s) != tc.want {
						t.Errorf("Tj = %q, want %q", s, tc.want)
					}
					found = true
				}
			}
			if !found {
				t.Errorf("no Tj found in %q", tc.src)
			}
		})
	}
}

// A comment inside a string must not cause the following ET/Q to be dropped —
// the exact failure mode documented at content_stream.rs:240.
func TestCommentDoesNotSwallowOperators(t *testing.T) {
	got := ops(t, "q BT (50% off) Tj ET Q")
	var sawET, sawQ bool
	for _, op := range got {
		switch op.Operator {
		case "ET":
			sawET = true
		case "Q":
			sawQ = true
		}
	}
	if !sawET || !sawQ {
		t.Errorf("ET=%v Q=%v; both must survive. ops=%v", sawET, sawQ, got)
	}
}

func TestLiteralStringEscapes(t *testing.T) {
	tests := []struct {
		src  string
		want []byte
	}{
		{`(a\nb)`, []byte("a\nb")},
		{`(a\tb)`, []byte("a\tb")},
		{`(\()`, []byte("(")},
		{`(\))`, []byte(")")},
		{`(\\)`, []byte(`\`)},
		{`(\101\102\103)`, []byte("ABC")},
		{`(\0)`, []byte{0}},
		{`(nested (parens) ok)`, []byte("nested (parens) ok")},
		{"(line\\\ncont)", []byte("linecont")},
		{`(\q)`, []byte("q")},
	}

	for _, tc := range tests {
		got := ops(t, tc.src+" Tj")
		if len(got) != 1 {
			t.Fatalf("%s: got %d ops", tc.src, len(got))
		}
		s := got[0].Operands[0].(String)
		if !bytes.Equal(s, tc.want) {
			t.Errorf("%s = %q, want %q", tc.src, s, tc.want)
		}
	}
}

func TestHexString(t *testing.T) {
	tests := []struct {
		src  string
		want []byte
	}{
		{"<48656C6C6F>", []byte("Hello")},
		{"<48 65 6C>", []byte("Hel")},
		{"<4>", []byte{0x40}},         // odd digit count pads with 0
		{"<416>", []byte{0x41, 0x60}}, // odd count, multi-byte
		{"<>", []byte{}},
	}
	for _, tc := range tests {
		got := ops(t, tc.src+" Tj")
		s := got[0].Operands[0].(String)
		if !bytes.Equal(s, tc.want) {
			t.Errorf("%s = %x, want %x", tc.src, s, tc.want)
		}
	}
}

func TestNameEscapes(t *testing.T) {
	got := ops(t, "/A#20B Do")
	if n := got[0].Operands[0].(Name); n != "A B" {
		t.Errorf("got %q, want %q", n, "A B")
	}
}

func TestNumbers(t *testing.T) {
	got := ops(t, "1 -2 +3 4.5 -0.5 .5 6. --7 cm")
	if len(got) != 1 {
		t.Fatalf("got %d ops, want 1", len(got))
	}
	wants := []float64{1, -2, 3, 4.5, -0.5, 0.5, 6, 7}
	if len(got[0].Operands) != len(wants) {
		t.Fatalf("got %d operands, want %d: %v", len(got[0].Operands), len(wants), got[0].Operands)
	}
	for i, w := range wants {
		f, ok := Float(got[0].Operands[i])
		if !ok || f != w {
			t.Errorf("operand %d = %v, want %v", i, got[0].Operands[i], w)
		}
	}
}

func TestTJArray(t *testing.T) {
	got := ops(t, "[(A) -250 (B)] TJ")
	if len(got) != 1 || got[0].Operator != "TJ" {
		t.Fatalf("got %v", got)
	}
	arr, ok := got[0].Operands[0].(Array)
	if !ok || len(arr) != 3 {
		t.Fatalf("operand = %v, want 3-element Array", got[0].Operands[0])
	}
	if s := arr[0].(String); string(s) != "A" {
		t.Errorf("arr[0] = %q", s)
	}
	if f, _ := Float(arr[1]); f != -250 {
		t.Errorf("arr[1] = %v, want -250", arr[1])
	}
}

func TestMarkedContentDict(t *testing.T) {
	got := ops(t, "/P <</MCID 3 /Lang (en)>> BDC")
	if got[0].Operator != "BDC" {
		t.Fatalf("got %v", got)
	}
	d, ok := got[0].Operands[1].(Dict)
	if !ok {
		t.Fatalf("operand 1 = %T, want Dict", got[0].Operands[1])
	}
	if v, _ := Int(d["MCID"]); v != 3 {
		t.Errorf("MCID = %v, want 3", d["MCID"])
	}
}

func TestInlineImage(t *testing.T) {
	// Payload deliberately contains the bytes "EI" undelimited, plus a "("
	// that would unbalance the lexer if the payload were tokenized.
	src := []byte("q BI /W 2 /H 2 /BPC 8 /CS /G ID \x00EI\xff(\x01 EI Q")
	got, err := Decode(src)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, op := range got {
		names = append(names, op.Operator)
	}
	want := []string{"q", "INLINE_IMAGE", "Q"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}

	d := got[1].Operands[0].(Dict)
	if v, _ := Int(d["W"]); v != 2 {
		t.Errorf("W = %v, want 2", d["W"])
	}
}

func TestBooleansAndNull(t *testing.T) {
	got := ops(t, "true false null gs")
	if len(got) != 1 {
		t.Fatalf("got %d ops: %v", len(got), got)
	}
	if b := got[0].Operands[0].(Bool); b != true {
		t.Error("operand 0 should be true")
	}
	if b := got[0].Operands[1].(Bool); b != false {
		t.Error("operand 1 should be false")
	}
	if _, ok := got[0].Operands[2].(Null); !ok {
		t.Error("operand 2 should be Null")
	}
}

func TestMalformedDoesNotHang(t *testing.T) {
	// Each of these is unterminated or structurally broken; Decode must return.
	for _, src := range []string{
		"BT (unterminated",
		"BT <deadbeef",
		"[[[[[[",
		"<<<<<<",
		"BI /W 1 ID nodata",
		")))]]]",
		"/",
		"<<",
	} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = Decode([]byte(src))
		}()
		<-done // a hang fails the test via the go test timeout
	}
}

func TestEmptyStream(t *testing.T) {
	got, err := Decode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d ops, want 0", len(got))
	}
}
