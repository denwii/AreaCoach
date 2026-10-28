package analysis

import "math"

// adaptMeasure fills in what the adaptive diagnosis needs from a window:
// how much longer or shorter than needed the jumps were (on both axes and
// per axis), and how sure each number is. The clicks that land far from the
// rest count less, so one bad note does not move the area.
func adaptMeasure(in []winNote, m Mapper, cp *CalibPoint) {
	if len(in) < 12 {
		return
	}
	// positions from their mean
	ns := append([]winNote(nil), in...)
	var mx, my float64
	for _, n := range ns {
		mx += n.qx
		my += n.qy
	}
	mx /= float64(len(ns))
	my /= float64(len(ns))
	for i := range ns {
		ns[i].qx -= mx
		ns[i].qy -= my
	}
	R := ns[0].R
	// a part of the screen where the clicks are always off the same way (a
	// corner the hand reaches badly) has its own offset: a habit there, which
	// is not the jumps being too long or short, and which no size or place
	// of the area would fix. Each of the nine parts gets an offset of its
	// own, held toward none unless its clicks clearly say so.
	noteCells(ns)
	cells := func(base []float64, n winNote, axis int) []float64 {
		out := make([]float64, len(base)+18)
		copy(out, base)
		out[len(base)+9*axis+n.cell] = 1
		return out
	}
	// both axes together: r = a + g·jump + h·position
	iso := func(n winNote) ([]float64, []float64) {
		return cells([]float64{1, 0, n.jx, n.qx}, n, 0), cells([]float64{0, 1, n.jy, n.qy}, n, 1)
	}
	// per axis: r = a + (gx, gy)·jump + (hx, hy)·position
	axes := func(n winNote) ([]float64, []float64) {
		return cells([]float64{1, 0, n.jx, 0, n.qx, 0}, n, 0), cells([]float64{0, 1, 0, n.jy, 0, n.qy}, n, 1)
	}
	bi, ci, wi, ok1 := robustFitR(ns, 4+18, iso, 4)
	ba, ca, _, ok2 := robustFitR(ns, 6+18, axes, 6)
	if !ok1 || !ok2 {
		return
	}
	// how much longer or shorter the movements are than needed: the part
	// measured from where the cursor was (aiming by movement) plus the part
	// measured from where the circles are (aiming by position on the
	// tablet); a hand used to another size shows in one, the other or both
	lg := func(cov [][]float64, beta []float64, i, j int) (float64, float64) {
		g := beta[i] + beta[j]
		se := math.Sqrt(math.Max(cov[i][i]+cov[j][j]+2*cov[i][j], 1e-12))
		f := math.Max(1+g, 0.2)
		return round(math.Log(f), 5), round(math.Min(se/f, 9), 5)
	}
	cp.G, cp.SG = lg(ci, bi, 2, 3)
	cp.GX, cp.SGX = lg(ca, ba, 2, 4)
	cp.GY, cp.SGY = lg(ca, ba, 3, 5)
	// the landing offset: where the clicks land on average, from the circles
	// (not the intercept of the fit: the movements are measured from where
	// the cursor was, so they already carry the offset)
	var sw float64
	mx, my = 0, 0
	for i, n := range ns {
		sw += wi[i]
		mx += wi[i] * n.rx
		my += wi[i] * n.ry
	}
	mx /= sw
	my /= sw
	var vx, vy, sw2 float64
	for i, n := range ns {
		vx += wi[i] * (n.rx - mx) * (n.rx - mx)
		vy += wi[i] * (n.ry - my) * (n.ry - my)
		sw2 += wi[i] * wi[i]
	}
	neff := sw * sw / sw2
	sex, sey := math.Sqrt(vx/sw/math.Max(1, neff-1)), math.Sqrt(vy/sw/math.Max(1, neff-1))
	// where the clicks land, part by part of the screen: when the parts
	// disagree (misses on one side only, say a corner hard to reach), the
	// average is not a shift of the whole area, and counts less as one
	var qs [4]struct{ w, x, y float64 }
	for i, n := range ns {
		k := 0
		if n.qx > 0 {
			k |= 1
		}
		if n.qy > 0 {
			k |= 2
		}
		qs[k].w += wi[i]
		qs[k].x += wi[i] * n.rx
		qs[k].y += wi[i] * n.ry
	}
	var qx2, qy2, nq float64
	for _, q := range qs {
		if q.w >= 5 {
			dx, dy := q.x/q.w-mx, q.y/q.w-my
			qx2 += dx * dx
			qy2 += dy * dy
			nq++
		}
	}
	if nq >= 3 {
		sex = math.Hypot(sex, math.Sqrt(qx2/(nq-1))/2)
		sey = math.Hypot(sey, math.Sqrt(qy2/(nq-1))/2)
	}
	dx, dy := m.Vec(mx*R, my*R)
	cp.DX, cp.DY = round(dx, 3), round(dy, 3)
	// how sure, on the tablet's x and y (the area may be rotated)
	cp.SDX = round(R*math.Hypot(m.A[0][0]*sex, m.A[0][1]*sey), 4)
	cp.SDY = round(R*math.Hypot(m.A[1][0]*sex, m.A[1][1]*sey), 4)
	_ = ci
}

// robustFit is a least-squares fit of both coordinates of the click errors,
// done twice: the second time the clicks far from the first fit weigh less.
// It returns the coefficients and their covariance.
func robustFit(ns []winNote, k int, rows func(winNote) ([]float64, []float64)) ([]float64, [][]float64, []float64, bool) {
	return robustFitR(ns, k, rows, k)
}

// robustFitR is robustFit where the coefficients from the index cells on
// are the offsets of the parts of the screen, held toward zero.
func robustFitR(ns []winNote, k int, rows func(winNote) ([]float64, []float64), cells int) ([]float64, [][]float64, []float64, bool) {
	w := make([]float64, len(ns))
	for i := range w {
		w[i] = 1
	}
	var beta []float64
	var inv [][]float64
	for pass := 0; pass < 2; pass++ {
		A := make([][]float64, k)
		for i := range A {
			A[i] = make([]float64, k)
		}
		B := make([][]float64, k)
		for i := range B {
			B[i] = []float64{0}
		}
		for i, n := range ns {
			rx, ry := rows(n)
			for _, rw := range [2]struct {
				x []float64
				y float64
			}{{rx, n.rx}, {ry, n.ry}} {
				for a := 0; a < k; a++ {
					for b := 0; b < k; b++ {
						A[a][b] += w[i] * rw.x[a] * rw.x[b]
					}
					B[a][0] += w[i] * rw.x[a] * rw.y
				}
			}
		}
		for j := 2; j < k; j++ {
			if j >= cells {
				A[j][j] += cellRidge
			} else {
				A[j][j] += 1e-3
			}
		}
		sol, ok := solve(A, B)
		if !ok {
			return nil, nil, nil, false
		}
		beta = make([]float64, k)
		for i := range beta {
			beta[i] = sol[i][0]
		}
		eye := make([][]float64, k)
		for i := range eye {
			eye[i] = make([]float64, k)
			eye[i][i] = 1
		}
		inv, ok = solve(A, eye)
		if !ok {
			return nil, nil, nil, false
		}
		// residuals
		var sq, sw float64
		res := make([]float64, len(ns))
		for i, n := range ns {
			rx, ry := rows(n)
			ex, ey := n.rx, n.ry
			for a := 0; a < k; a++ {
				ex -= beta[a] * rx[a]
				ey -= beta[a] * ry[a]
			}
			res[i] = math.Hypot(ex, ey)
			sq += w[i] * res[i] * res[i]
			sw += w[i]
		}
		dof := 2*sw - float64(cells)
		if dof < 4 {
			return nil, nil, nil, false
		}
		s2 := sq / dof
		if pass == 1 {
			for a := range inv {
				for b := range inv[a] {
					inv[a][b] *= s2
				}
			}
			break
		}
		// Huber weights on the distance from the fit
		c := 1.8 * math.Sqrt(2*s2)
		for i := range w {
			if res[i] > c {
				w[i] = c / res[i]
			}
		}
	}
	return beta, inv, w, true
}
