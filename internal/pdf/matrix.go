package pdf

import "math"

// matrix is a 2D affine transform [a b c d e f] representing
//
//	| a  b  0 |
//	| c  d  0 |
//	| e  f  1 |
type matrix [6]float32

// identity is the no-op transform.
var identity = matrix{1, 0, 0, 1, 0, 0}

// mul returns m × n. Order matters: the receiver is applied first.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// apply transforms the point (u, v).
func (m matrix) apply(u, v float32) (float32, float32) {
	return u*m[0] + v*m[2] + m[4], u*m[1] + v*m[3] + m[5]
}

// riseAdjusted offsets the matrix by a text rise (Ts), which shifts the
// baseline for super/subscripts without disturbing the scale components.
func (m matrix) riseAdjusted(rise float32) matrix {
	if rise == 0 {
		return m
	}
	m[4] += rise * m[2]
	m[5] += rise * m[3]
	return m
}

// imageBBox computes the page-space axis-aligned bounding box of an Image
// XObject invoked under this CTM.
//
// Per the spec an image always renders into the unit square (0,0)–(1,1) in its
// own space, and Do applies the CTM to place it. For the common axis-aligned
// case the CTM reduces to [w 0 0 h x y] and the bbox is just (x, y, w, h); for
// rotated or sheared images all four corners are transformed so the caller
// always receives an upright rectangle.
func (m matrix) imageBBox() (x, y, w, h float32) {
	type point struct{ x, y float32 }
	var corners [4]point
	corners[0].x, corners[0].y = m.apply(0, 0)
	corners[1].x, corners[1].y = m.apply(1, 0)
	corners[2].x, corners[2].y = m.apply(1, 1)
	corners[3].x, corners[3].y = m.apply(0, 1)

	xMin, xMax := corners[0].x, corners[0].x
	yMin, yMax := corners[0].y, corners[0].y
	for _, c := range corners[1:] {
		xMin = minF32(xMin, c.x)
		xMax = maxF32(xMax, c.x)
		yMin = minF32(yMin, c.y)
		yMax = maxF32(yMax, c.y)
	}
	return xMin, yMin, xMax - xMin, yMax - yMin
}

// effectiveFontSize scales the nominal Tf size by the text matrix. The larger
// of the two axis scales is used; for non-rotated text they are equal.
func effectiveFontSize(base float32, m matrix) float32 {
	scaleX := float32(math.Hypot(float64(m[0]), float64(m[1])))
	scaleY := float32(math.Hypot(float64(m[2]), float64(m[3])))
	return base * maxF32(scaleX, scaleY)
}

func minF32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
