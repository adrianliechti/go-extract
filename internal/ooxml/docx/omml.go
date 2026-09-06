package docx

import (
	"encoding/xml"
	"strings"
)

// OMML (Office MathML) carries equations in Word documents. Extracting only
// the m:t character data loses the structure that gives an equation meaning:
// "r²" flattens to "r2", which reads as a product rather than a power.
//
// This renders OMML to LaTeX instead, which is unambiguous and is what
// downstream consumers of extracted Markdown expect. Constructs without a
// LaTeX equivalent degrade to their contained text rather than being dropped.

// mathNode is a parsed OMML element. Children hold the operands whose meaning
// depends on the node kind.
type mathNode struct {
	kind string // element local name, e.g. "f", "sSup", "r"
	text string // literal characters, for "r" nodes
	// args holds the node's operands keyed by their OMML role ("num", "den",
	// "e", "sup", "sub", "fName", "deg").
	args map[string][]mathNode
	// seq holds children in document order, for containers.
	seq []mathNode
	// rows holds one entry per m:e for equation arrays, where each m:e is a
	// row rather than a term. args["e"] would flatten them together.
	rows [][]mathNode
	// chr is the n-ary operator character (∑, ∫, ∏).
	chr string
}

// parseOMML consumes an m:oMath or m:oMathPara element and returns its LaTeX.
// display reports whether it should be typeset as a block.
func parseOMML(d *xml.Decoder, start xml.StartElement) (latex string, display bool) {
	display = start.Name.Local == "oMathPara"
	nodes := parseMathNodes(d, start)

	var b strings.Builder
	renderNodes(nodes, &b)
	return strings.TrimSpace(b.String()), display
}

// parseMathNodes reads the children of an OMML element into a node sequence.
func parseMathNodes(d *xml.Decoder, start xml.StartElement) []mathNode {
	var out []mathNode

	for {
		tok, err := d.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return out
			}
		case xml.StartElement:
			switch t.Name.Local {
			// Property elements describe styling, not structure.
			case "ctrlPr", "fPr", "sSupPr", "sSubPr", "sSubSupPr", "naryPr",
				"dPr", "radPr", "funcPr", "eqArrPr", "boxPr", "barPr", "accPr",
				"limLowPr", "limUppPr", "mPr", "rPr", "oMathParaPr", "argPr":
				// chr carries the n-ary operator and must survive.
				if chr, ok := scanForChr(d, t); ok {
					out = append(out, mathNode{kind: "chr", text: chr})
				}
			case "r":
				out = append(out, mathNode{kind: "r", text: parseMathRun(d, t)})
			case "t":
				var s string
				if err := d.DecodeElement(&s, &t); err == nil {
					out = append(out, mathNode{kind: "r", text: s})
				}
			default:
				out = append(out, parseMathElement(d, t))
			}
		}
	}
}

// scanForChr consumes a property element, returning an n-ary operator
// character if it declares one.
func scanForChr(d *xml.Decoder, start xml.StartElement) (string, bool) {
	chr := ""
	for {
		tok, err := d.Token()
		if err != nil {
			return chr, chr != ""
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return chr, chr != ""
			}
		case xml.StartElement:
			if t.Name.Local == "chr" {
				if v := attr(t, "val"); v != "" {
					chr = v
				}
			}
		}
	}
}

// parseMathElement reads a structural element and buckets its children by role.
func parseMathElement(d *xml.Decoder, start xml.StartElement) mathNode {
	n := mathNode{kind: start.Name.Local, args: map[string][]mathNode{}}

	for {
		tok, err := d.Token()
		if err != nil {
			return n
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return n
			}
		case xml.StartElement:
			switch t.Name.Local {
			case "num", "den", "e", "sup", "sub", "fName", "deg", "lim":
				children := parseMathNodes(d, t)
				if t.Name.Local == "e" && (n.kind == "eqArr" || n.kind == "m" || n.kind == "mr") {
					n.rows = append(n.rows, children)
					continue
				}
				n.args[t.Name.Local] = append(n.args[t.Name.Local], children...)
			case "ctrlPr", "fPr", "sSupPr", "sSubPr", "sSubSupPr", "naryPr",
				"dPr", "radPr", "funcPr", "eqArrPr", "boxPr", "barPr", "accPr",
				"limLowPr", "limUppPr", "mPr", "rPr", "argPr":
				if chr, ok := scanForChr(d, t); ok {
					n.chr = chr
				}
			case "r":
				n.seq = append(n.seq, mathNode{kind: "r", text: parseMathRun(d, t)})
			default:
				n.seq = append(n.seq, parseMathElement(d, t))
			}
		}
	}
}

// parseMathRun concatenates the text of an m:r element.
func parseMathRun(d *xml.Decoder, start xml.StartElement) string {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return b.String()
		}
		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				return b.String()
			}
		case xml.StartElement:
			if t.Name.Local == "t" {
				var s string
				if err := d.DecodeElement(&s, &t); err == nil {
					b.WriteString(s)
				}
			}
		}
	}
}

// ── rendering ────────────────────────────────────────────────────────

func renderNodes(nodes []mathNode, b *strings.Builder) {
	for i := range nodes {
		renderNode(&nodes[i], b)
	}
}

func render(nodes []mathNode) string {
	var b strings.Builder
	renderNodes(nodes, &b)
	return b.String()
}

func renderNode(n *mathNode, b *strings.Builder) {
	switch n.kind {
	case "r", "chr":
		b.WriteString(latexText(n.text))

	case "f": // fraction
		b.WriteString("\\frac{" + render(n.args["num"]) + "}{" + render(n.args["den"]) + "}")

	case "sSup": // superscript
		b.WriteString(group(render(n.args["e"])) + "^{" + render(n.args["sup"]) + "}")

	case "sSub": // subscript
		b.WriteString(group(render(n.args["e"])) + "_{" + render(n.args["sub"]) + "}")

	case "sSubSup": // both
		b.WriteString(group(render(n.args["e"])) +
			"_{" + render(n.args["sub"]) + "}" +
			"^{" + render(n.args["sup"]) + "}")

	case "rad": // radical
		if deg := render(n.args["deg"]); deg != "" {
			b.WriteString("\\sqrt[" + deg + "]{" + render(n.args["e"]) + "}")
		} else {
			b.WriteString("\\sqrt{" + render(n.args["e"]) + "}")
		}

	case "nary": // sum, integral, product
		op := naryOperator(n.chr)
		b.WriteString(op)
		if sub := render(n.args["sub"]); sub != "" {
			b.WriteString("_{" + sub + "}")
		}
		if sup := render(n.args["sup"]); sup != "" {
			b.WriteString("^{" + sup + "}")
		}
		if e := render(n.args["e"]); e != "" {
			b.WriteString("{" + e + "}")
		}

	case "d": // delimiters
		b.WriteString("\\left(" + render(n.args["e"]) + "\\right)")

	case "func": // named function
		b.WriteString(latexFunc(render(n.args["fName"])) + "{" + render(n.args["e"]) + "}")

	case "limLow": // lower limit
		b.WriteString(render(n.args["e"]) + "_{" + render(n.args["lim"]) + "}")

	case "limUpp": // upper limit
		b.WriteString(render(n.args["e"]) + "^{" + render(n.args["lim"]) + "}")

	case "eqArr", "m", "mr": // aligned equation array / matrix
		parts := make([]string, 0, len(n.rows))
		for _, row := range n.rows {
			parts = append(parts, render(row))
		}
		if len(parts) == 0 {
			break
		}
		if len(parts) == 1 {
			b.WriteString(parts[0])
			break
		}
		b.WriteString("\\begin{aligned}" + strings.Join(parts, " \\\\ ") + "\\end{aligned}")

	case "acc": // accent
		b.WriteString("\\hat{" + render(n.args["e"]) + "}")

	case "bar":
		b.WriteString("\\overline{" + render(n.args["e"]) + "}")

	default:
		// Unknown construct: emit its contents so no characters are lost.
		b.WriteString(render(n.args["e"]))
		renderNodes(n.seq, b)
	}

	// Structural elements may also carry loose children.
	if n.kind != "r" && n.kind != "chr" && len(n.seq) > 0 && !rendersSeq(n.kind) {
		renderNodes(n.seq, b)
	}
}

// rendersSeq reports kinds whose default branch already emitted seq.
func rendersSeq(kind string) bool {
	switch kind {
	case "f", "sSup", "sSub", "sSubSup", "rad", "nary", "d", "func",
		"limLow", "limUpp", "eqArr", "m", "mr", "acc", "bar":
		return false
	}
	return true
}

// group wraps a base in braces when it is longer than a single token, so
// "ab^{2}" does not silently mean "a·b²".
func group(s string) string {
	if len(s) <= 1 {
		return s
	}
	if strings.HasPrefix(s, "\\") && !strings.ContainsAny(s, " {}^_") {
		return s // a bare command such as \pi needs no braces
	}
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return s
	}
	return "{" + s + "}"
}

// latexFunc turns a function name into its LaTeX command so "cos" typesets as
// an operator rather than a product of three variables.
func latexFunc(name string) string {
	trimmed := strings.TrimSpace(name)
	switch trimmed {
	case "sin", "cos", "tan", "cot", "sec", "csc",
		"sinh", "cosh", "tanh", "coth",
		"arcsin", "arccos", "arctan",
		"log", "ln", "lg", "exp", "det", "dim", "gcd",
		"lim", "max", "min", "sup", "inf", "deg", "ker", "arg":
		return "\\" + trimmed
	}
	return name
}

// naryOperator maps an n-ary operator character to its LaTeX command.
func naryOperator(chr string) string {
	switch chr {
	case "∑", "":
		return "\\sum" // the default n-ary operator is summation
	case "∏":
		return "\\prod"
	case "∐":
		return "\\coprod"
	case "∫":
		return "\\int"
	case "∬":
		return "\\iint"
	case "∭":
		return "\\iiint"
	case "∮":
		return "\\oint"
	case "⋃":
		return "\\bigcup"
	case "⋂":
		return "\\bigcap"
	}
	return latexText(chr)
}

// latexText converts literal equation characters to LaTeX, mapping symbols
// that have commands and escaping the characters LaTeX reserves.
func latexText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if cmd, ok := mathSymbols[r]; ok {
			b.WriteString(cmd)
			continue
		}
		switch r {
		case '{', '}', '%', '$', '&', '#', '_':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\\':
			b.WriteString("\\backslash ")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
