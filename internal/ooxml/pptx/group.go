package pptx

import "math"

type affine struct{ a, b, c, d, x, y float64 }

func identity() affine { return affine{a: 1, d: 1} }
func (m affine) compose(n affine) affine {
	return affine{m.a*n.a + m.c*n.b, m.b*n.a + m.d*n.b, m.a*n.c + m.c*n.d, m.b*n.c + m.d*n.d, m.a*n.x + m.c*n.y + m.x, m.b*n.x + m.d*n.y + m.y}
}
func (m affine) finite() bool {
	for _, n := range []float64{m.a, m.b, m.c, m.d, m.x, m.y} {
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	return true
}
func (m affine) topLeft(x, y, w, h float64) (float64, float64) {
	left, top := math.Inf(1), math.Inf(1)
	for _, p := range [][2]float64{{x, y}, {x + w, y}, {x, y + h}, {x + w, y + h}} {
		left = math.Min(left, m.a*p[0]+m.c*p[1]+m.x)
		top = math.Min(top, m.b*p[0]+m.d*p[1]+m.y)
	}
	return left, top
}

type groupPoint struct {
	X float64 `xml:"x,attr"`
	Y float64 `xml:"y,attr"`
}
type groupExtent struct {
	X float64 `xml:"cx,attr"`
	Y float64 `xml:"cy,attr"`
}
type groupTransform struct {
	Off      groupPoint  `xml:"off"`
	Ext      groupExtent `xml:"ext"`
	ChildOff groupPoint  `xml:"chOff"`
	ChildExt groupExtent `xml:"chExt"`
	FlipH    string      `xml:"flipH,attr"`
	FlipV    string      `xml:"flipV,attr"`
	Rotation float64     `xml:"rot,attr"`
}

// DrawingML CT_GroupTransform2D maps chOff/chExt into off/ext, then flips
// and rotates about the group's centre. Parent matrices compose outside the
// child matrix. Sorting uses the transformed box, not a mirrored corner.
func (t groupTransform) matrix() affine {
	sx, sy := 1.0, 1.0
	if t.ChildExt.X != 0 {
		sx = t.Ext.X / t.ChildExt.X
	}
	if t.ChildExt.Y != 0 {
		sy = t.Ext.Y / t.ChildExt.Y
	}
	m := affine{a: sx, d: sy, x: t.Off.X - t.ChildOff.X*sx, y: t.Off.Y - t.ChildOff.Y*sy}
	cx, cy := t.Off.X+t.Ext.X/2, t.Off.Y+t.Ext.Y/2
	fx, fy := 1.0, 1.0
	if on(t.FlipH) {
		fx = -1
	}
	if on(t.FlipV) {
		fy = -1
	}
	sin, cos := math.Sincos(t.Rotation / 60000 * math.Pi / 180)
	r := affine{a: cos * fx, b: sin * fx, c: -sin * fy, d: cos * fy}
	r.x, r.y = cx-r.a*cx-r.c*cy, cy-r.b*cx-r.d*cy
	return r.compose(m)
}
