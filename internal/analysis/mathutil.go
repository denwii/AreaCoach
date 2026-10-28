package analysis

import (
	"math"
	"math/rand"
	"sort"
)

// solve solves A x = b for small dense systems (Gaussian elimination with
// partial pivoting). A is n×n row-major, b has m right-hand sides (n×m).
// Returns false if the system is singular.
func solve(A [][]float64, B [][]float64) ([][]float64, bool) {
	n := len(A)
	m := len(B[0])
	a := make([][]float64, n)
	b := make([][]float64, n)
	for i := range A {
		a[i] = append([]float64(nil), A[i]...)
		b[i] = append([]float64(nil), B[i]...)
	}
	for c := 0; c < n; c++ {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		if math.Abs(a[p][c]) < 1e-12 {
			return nil, false
		}
		a[c], a[p] = a[p], a[c]
		b[c], b[p] = b[p], b[c]
		for r := c + 1; r < n; r++ {
			f := a[r][c] / a[c][c]
			if f == 0 {
				continue
			}
			for k := c; k < n; k++ {
				a[r][k] -= f * a[c][k]
			}
			for k := 0; k < m; k++ {
				b[r][k] -= f * b[c][k]
			}
		}
	}
	x := make([][]float64, n)
	for i := range x {
		x[i] = make([]float64, m)
	}
	for k := 0; k < m; k++ {
		for r := n - 1; r >= 0; r-- {
			s := b[r][k]
			for c := r + 1; c < n; c++ {
				s -= a[r][c] * x[c][k]
			}
			x[r][k] = s / a[r][r]
		}
	}
	return x, true
}

// nelderMead minimises f starting at x0 with initial step sizes.
func nelderMead(f func([]float64) float64, x0, step []float64, iters int) ([]float64, float64) {
	n := len(x0)
	pts := make([][]float64, n+1)
	vals := make([]float64, n+1)
	pts[0] = append([]float64(nil), x0...)
	vals[0] = f(pts[0])
	for i := 0; i < n; i++ {
		p := append([]float64(nil), x0...)
		p[i] += step[i]
		pts[i+1] = p
		vals[i+1] = f(p)
	}
	idx := make([]int, n+1)
	cen := make([]float64, n)
	try := func(coef float64, worst []float64) ([]float64, float64) {
		p := make([]float64, n)
		for i := range p {
			p[i] = cen[i] + coef*(worst[i]-cen[i])
		}
		return p, f(p)
	}
	for it := 0; it < iters; it++ {
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return vals[idx[a]] < vals[idx[b]] })
		best, worst, second := idx[0], idx[n], idx[n-1]
		if math.Abs(vals[worst]-vals[best]) < 1e-10*(1+math.Abs(vals[best])) && it > 20 {
			break
		}
		for i := range cen {
			cen[i] = 0
		}
		for _, k := range idx[:n] {
			for i := range cen {
				cen[i] += pts[k][i] / float64(n)
			}
		}
		xr, fr := try(-1, pts[worst])
		switch {
		case fr < vals[best]:
			xe, fe := try(-2, pts[worst])
			if fe < fr {
				pts[worst], vals[worst] = xe, fe
			} else {
				pts[worst], vals[worst] = xr, fr
			}
		case fr < vals[second]:
			pts[worst], vals[worst] = xr, fr
		default:
			xc, fc := try(0.5, pts[worst])
			if fc < vals[worst] {
				pts[worst], vals[worst] = xc, fc
			} else {
				for _, k := range idx[1:] {
					for i := range pts[k] {
						pts[k][i] = pts[best][i] + 0.5*(pts[k][i]-pts[best][i])
					}
					vals[k] = f(pts[k])
				}
			}
		}
	}
	bi := 0
	for i := range vals {
		if vals[i] < vals[bi] {
			bi = i
		}
	}
	return pts[bi], vals[bi]
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	pos := q * float64(len(c)-1)
	i := int(pos)
	if i >= len(c)-1 {
		return c[len(c)-1]
	}
	f := pos - float64(i)
	return c[i]*(1-f) + c[i+1]*f
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// glNodes are 32-point Gauss-Legendre nodes/weights on [-1, 1].
var glX, glW = gaussLegendre(16)

func gaussLegendre(n int) ([]float64, []float64) {
	x := make([]float64, n)
	w := make([]float64, n)
	for i := 0; i < (n+1)/2; i++ {
		z := math.Cos(math.Pi * (float64(i) + 0.75) / (float64(n) + 0.5))
		var pp float64
		for it := 0; it < 100; it++ {
			p1, p2 := 1.0, 0.0
			for j := 0; j < n; j++ {
				p3 := p2
				p2 = p1
				p1 = ((2*float64(j)+1)*z*p2 - float64(j)*p3) / float64(j+1)
			}
			pp = float64(n) * (z*p1 - p2) / (z*z - 1)
			z1 := z
			z = z1 - p1/pp
			if math.Abs(z-z1) < 1e-15 {
				break
			}
		}
		x[i], x[n-1-i] = -z, z
		w[i] = 2 / ((1 - z*z) * pp * pp)
		w[n-1-i] = w[i]
	}
	return x, w
}

// hitProbability returns the probability that a click with Gaussian error
// (sd sPar along the movement, sPerp across it, mean offset mu along the
// movement) lands inside a circle of radius R.
func hitProbability(R, sPar, sPerp, mu float64) float64 {
	if R <= 0 {
		return 0
	}
	if sPar < 1e-6 {
		sPar = 1e-6
	}
	if sPerp < 1e-6 {
		sPerp = 1e-6
	}
	// far inside: the circle covers more than 6 standard deviations
	if R-math.Abs(mu) > 6.5*math.Max(sPar, sPerp) {
		return 1
	}
	total := 0.0
	for i, xi := range glX {
		x := xi * R
		half := math.Sqrt(math.Max(R*R-x*x, 0))
		px := math.Exp(-0.5*((x-mu)/sPar)*((x-mu)/sPar)) / (sPar * math.Sqrt(2*math.Pi))
		py := math.Erf(half / (sPerp * math.Sqrt2))
		total += glW[i] * px * py
	}
	p := total * R
	if p > 1 {
		p = 1
	}
	if p < 0 {
		p = 0
	}
	return p
}

// resampleClusters draws cluster multiplicities for one bootstrap replicate.
func resampleClusters(rng *rand.Rand, n int) []int {
	m := make([]int, n)
	for i := 0; i < n; i++ {
		m[rng.Intn(n)]++
	}
	return m
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func round(x float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(x*p) / p
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
