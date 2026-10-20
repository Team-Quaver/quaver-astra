package scene

import (
	"math"
	"math/rand/v2"
	"testing"
)

func TestAffineCompositionInverseAndSingular(t *testing.T) {
	m := Translation(30, 20).Mul(Rotation(90)).Mul(Scaling(2, -3))
	x, y := m.Point(4, 5)
	if math.Abs(float64(x-45)) > 1e-4 || math.Abs(float64(y-28)) > 1e-4 {
		t.Fatalf("composed point %v,%v", x, y)
	}
	i, ok := m.Inverse()
	if !ok {
		t.Fatal("invertible matrix rejected")
	}
	x, y = i.Point(x, y)
	if math.Abs(float64(x-4)) > 1e-4 || math.Abs(float64(y-5)) > 1e-4 {
		t.Fatalf("inverse point %v,%v", x, y)
	}
	if _, ok := Scaling(0, 1).Inverse(); ok {
		t.Fatal("singular matrix inverted")
	}
	b := m.Bounds(Rect{W: 4, H: 5})
	if math.Abs(float64(b.W-15)) > 1e-4 || math.Abs(float64(b.H-8)) > 1e-4 {
		t.Fatalf("bounds %v", b)
	}
	if (Affine{}).Mul(m) != m || m.Mul(Affine{}) != m {
		t.Fatal("zero value is not identity")
	}
}

// Bounds and Mul of maps that only move or scale take shortcuts: they
// must give what the corners and the general product give.
func TestAffineShortcutsMatch(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	v := func() float32 {
		switch r.IntN(4) {
		case 0:
			return 0
		case 1:
			return float32(r.IntN(200) - 100)
		}
		return (r.Float32() - 0.5) * 2000
	}
	for range 100000 {
		m := Matrix(1, 0, 0, 1, v(), v())
		if r.IntN(2) == 0 {
			m = Matrix(v()/100, 0, 0, v()/100, v(), v())
		}
		n := Translation(v(), v())
		rect := Rect{v(), v(), v(), v()}
		got, want := m.Bounds(rect), cornerBounds(m, rect)
		if got != want {
			t.Fatalf("%v.Bounds(%v) = %v, corners give %v", m, rect, got, want)
		}
		if m.moves() && n.Set {
			want := Matrix(m.A*n.A+m.C*n.B, m.B*n.A+m.D*n.B, m.A*n.C+m.C*n.D, m.B*n.C+m.D*n.D,
				m.A*n.X+m.C*n.Y+m.X, m.B*n.X+m.D*n.Y+m.Y)
			if got := m.Mul(n); got != want {
				t.Fatalf("%v.Mul(%v) = %v, want %v", m, n, got, want)
			}
		}
	}
}

// cornerBounds is Bounds from the corners, for maps without perspective.
func cornerBounds(m Affine, r Rect) Rect {
	if !m.Set {
		return r
	}
	corners := [4][2]float32{{r.X, r.Y}, {r.X + r.W, r.Y}, {r.X, r.Y + r.H}, {r.X + r.W, r.Y + r.H}}
	x0, y0 := m.Point(r.X, r.Y)
	x1, y1 := x0, y0
	for _, p := range corners[1:] {
		x, y := m.Point(p[0], p[1])
		x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
	}
	return Rect{x0, y0, x1 - x0, y1 - y0}
}
