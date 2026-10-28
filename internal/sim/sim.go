// Package sim simulates a tablet player with known aim characteristics.
// It is used to verify that the analysis recovers the truth, and to build
// demo data.
package sim

import (
	"math"
	"math/rand"

	"areacoach/internal/analysis"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

// Player describes the true behaviour of a simulated player in tablet mm.
type Player struct {
	Offset   [2]float64    // constant landing offset (mm)
	Av       [2][2]float64 // vector gain (jumps)
	Ap       [2][2]float64 // positional gain around the area centre
	APar     float64       // steadiness noise (mm)
	BPar     float64       // noise per mm of movement
	CPar     float64       // speed noise coefficient
	APerp    float64
	BPerp    float64
	CPerp    float64
	P        float64 // speed exponent
	TimingSD float64 // ms
	MissRate float64 // probability of not pressing at all (rhythm mistakes)
	Rng      *rand.Rand
	// Hand, when set, decides the over/undershoot and the landing offset
	// from the areas instead of Av and gainOf
	Hand *Hand
	// habits that do not depend on the area: clicking this many radii
	// before the centre of the circle along the movement (on the screen),
	// and an over/undershoot of the movements per axis (share)
	Lazy   float64
	HabitG [2]float64
	// the steadiness noise grows on areas narrower than ShakeRef mm as
	// (ShakeRef / width)^ShakeExp (and shrinks on wider ones)
	ShakeRef, ShakeExp float64
	// the hand turns around a pivot (the wrist, tablet mm): a movement comes
	// out turned clockwise by Kappa·(φ − Phi0), φ being how far around the
	// pivot it happens (radians, 0 straight in front of it, + to its right)
	Pivot [2]float64
	Kappa float64
	Phi0  float64
	// the circles in the bottom-right part of the screen (beyond 62% of the
	// playfield both ways) are hit this far off (mm): a corner the hand
	// reaches badly, whatever the area
	Corner [2]float64
	// the pen trembles this much (mm, SD) all the time, frame to frame
	Tremor float64
	// the hand is steadiest with the area centred at PlaceRef (tablet mm):
	// the scatter grows by exp(PlaceK · distance²) away from it
	PlaceRef [2]float64
	PlaceK   float64
}

// PivotAngle is how far around the pivot p a tablet point is (radians).
func PivotAngle(p [2]float64, x, y float64) float64 { return math.Atan2(x-p[0], p[1]-y) }

// Hand is how a player's hand relates to the tablet. It is used to one area
// (its size and where it sits): with a different one it keeps aiming partly
// as with that, for good (Residual), and right after a change also partly as
// with the previous area, fading over Fade ms.
type Hand struct {
	Natural  otd.Rect
	Residual float64 // share of the size difference left once used to an area
	CenterR  float64 // the same for the position
	Fade     float64 // ms
	// share of the size difference that shows in where the hand goes
	// (aiming by position on the tablet) rather than in how far it moves
	// from where it is (aiming by movement)
	Absolute float64
}

// mismatch is the over/undershoot of jumps (per axis) and the landing
// offset (mm) with area a, set since ms after the area prev.
func (h *Hand) mismatch(a, prev otd.Rect, since float64) (gx, gy, ox, oy float64) {
	f := 0.0
	if prev.Valid() && h.Fade > 0 && !math.IsInf(since, 1) {
		f = math.Exp(-since / h.Fade)
	}
	n := h.Natural
	gx = h.Residual*(n.W/a.W-1) + (1-h.Residual)*f*(prev.W/a.W-1)
	gy = h.Residual*(n.H/a.H-1) + (1-h.Residual)*f*(prev.H/a.H-1)
	ox = h.CenterR*(n.X-a.X) + (1-h.CenterR)*f*(prev.X-a.X)
	oy = h.CenterR*(n.Y-a.Y) + (1-h.CenterR)*f*(prev.Y-a.Y)
	if !prev.Valid() {
		gx, gy = h.Residual*(n.W/a.W-1), h.Residual*(n.H/a.H-1)
		ox, oy = h.CenterR*(n.X-a.X), h.CenterR*(n.Y-a.Y)
	}
	return
}

func (pl *Player) gauss() float64 { return pl.Rng.NormFloat64() }

type inv struct {
	a  [2][2]float64
	b  [2]float64
	ia [2][2]float64
}

func newInv(m analysis.Mapper) inv {
	det := m.A[0][0]*m.A[1][1] - m.A[0][1]*m.A[1][0]
	return inv{a: m.A, b: m.B, ia: [2][2]float64{{m.A[1][1] / det, -m.A[0][1] / det}, {-m.A[1][0] / det, m.A[0][0] / det}}}
}

func (v inv) toPx(x, y float64) (float64, float64) {
	x -= v.b[0]
	y -= v.b[1]
	return v.ia[0][0]*x + v.ia[0][1]*y, v.ia[1][0]*x + v.ia[1][1]*y
}

func (v inv) toMM(x, y float64) (float64, float64) {
	return v.a[0][0]*x + v.a[0][1]*y + v.b[0], v.a[1][0]*x + v.a[1][1]*y + v.b[1]
}

func (v inv) vecMM(x, y float64) (float64, float64) {
	return v.a[0][0]*x + v.a[0][1]*y, v.a[1][0]*x + v.a[1][1]*y
}

// Change is an area change during a play, at a map time (ms).
type Change struct {
	T    float64
	Area otd.Rect
}

// Play generates replay frames for a map with the given mods and setup.
func (pl *Player) Play(bm *osu.Beatmap, mods uint32, setup analysis.Setup) []osu.Frame {
	return pl.play(bm, mods, setup, nil, 0, nil)
}

// PlayLive plays a map while the area changes. After each change the player
// keeps aiming as with the previous area for a while (adapt ms, fading
// exponentially); gainOf gives the over/undershoot of jumps for an area
// width, given the ms since it was set (nil: the player's own Av).
func (pl *Player) PlayLive(bm *osu.Beatmap, mods uint32, setup analysis.Setup, changes []Change, adapt float64, gainOf func(w, since float64) float64) []osu.Frame {
	return pl.play(bm, mods, setup, changes, adapt, gainOf)
}

func (pl *Player) play(bm *osu.Beatmap, mods uint32, setup analysis.Setup, changes []Change, adapt float64, gainOf func(w, since float64) float64) []osu.Frame {
	objs, d := bm.Objects(mods)
	type state struct {
		iv    inv
		area  otd.Rect
		since float64
	}
	states := []state{{iv: newInv(setup.Mapper()), area: setup.Area, since: math.Inf(-1)}}
	for _, c := range changes {
		su := setup
		su.Area = c.Area
		states = append(states, state{iv: newInv(su.Mapper()), area: c.Area, since: c.T})
	}
	at := func(t float64) int {
		k := 0
		for i := 1; i < len(states); i++ {
			if states[i].since <= t {
				k = i
			}
		}
		return k
	}
	iv := states[0].iv
	rate := d.Rate
	var frames []osu.Frame
	penX, penY := iv.toMM(256, 192)
	t := 0.0
	if len(objs) > 0 {
		t = math.Floor(objs[0].Time - 1500)
	}
	keyAlt := false
	var trX, trY float64
	addFrame := func(tt, x, y float64, keys int32) {
		if pl.Tremor > 0 {
			// a tremble of a few hertz: each frame keeps part of the last
			const keep = 0.4
			k := pl.Tremor * math.Sqrt(1-keep*keep)
			trX = keep*trX + k*pl.Rng.NormFloat64()
			trY = keep*trY + k*pl.Rng.NormFloat64()
			x, y = x+trX, y+trY
		}
		px, py := states[at(tt)].iv.toPx(x, y)
		ft := math.Round(tt)
		if n := len(frames); n > 0 && ft <= frames[n-1].T {
			ft = frames[n-1].T + 1
		}
		frames = append(frames, osu.Frame{T: ft, X: float32(px), Y: float32(py), Keys: keys})
	}
	addFrame(t, penX, penY, 0)
	held := int32(0)
	releaseAt := -1.0
	moveTo := func(t0, t1, x0, y0, x1, y1 float64) {
		// minimum-jerk movement sampled every ~16 ms
		dur := t1 - t0
		for ft := t0 + 16; ft < t1-0.5; ft += 16 {
			if releaseAt > 0 && ft >= releaseAt {
				held = 0
				releaseAt = -1
			}
			s := (ft - t0) / dur
			p := 10*s*s*s - 15*s*s*s*s + 6*s*s*s*s*s
			addFrame(ft, x0+(x1-x0)*p, y0+(y1-y0)*p, held)
		}
	}
	for i := range objs {
		o := &objs[i]
		k := at(o.Time)
		st := states[k]
		iv = st.iv
		ax, ay := st.area.X, st.area.Y
		if o.Kind == osu.KindSpinner {
			cx, cy := iv.toMM(256, 192)
			moveTo(t, o.Time, penX, penY, cx+10, cy)
			held = osu.KeyK1 | osu.KeyM1
			for ft := o.Time; ft < o.EndTime; ft += 16 {
				a := (ft - o.Time) / 60
				addFrame(ft, cx+10*math.Cos(a), cy+10*math.Sin(a), held)
			}
			held = 0
			penX, penY = cx+10, cy
			t = o.EndTime
			continue
		}
		sp := o.StackedPos(d.StackOffset)
		tx, ty := iv.toMM(sp.X, sp.Y)
		if k > 0 && adapt > 0 {
			// still partly aiming as with the previous area
			f := math.Exp(-(o.Time - st.since) / rate / adapt)
			ox, oy := states[k-1].iv.toMM(sp.X, sp.Y)
			tx, ty = f*ox+(1-f)*tx, f*oy+(1-f)*ty
		}
		av := pl.Av
		var hox, hoy float64
		ap := pl.Ap
		if pl.Hand != nil {
			since := math.Inf(1)
			prev := otd.Rect{}
			if k > 0 {
				since = (o.Time - st.since) / rate
				prev = states[k-1].area
			}
			gx, gy, ox, oy := pl.Hand.mismatch(st.area, prev, since)
			ab := pl.Hand.Absolute
			// gx, gy are along the area's own width and height: on the
			// tablet they turn with the area
			th := st.area.Rot * math.Pi / 180
			c, sn := math.Cos(th), math.Sin(th)
			g := [2][2]float64{{c*c*gx + sn*sn*gy, c * sn * (gx - gy)}, {c * sn * (gx - gy), sn*sn*gx + c*c*gy}}
			av = [2][2]float64{{(1 - ab) * g[0][0], (1 - ab) * g[0][1]}, {(1 - ab) * g[1][0], (1 - ab) * g[1][1]}}
			for i := 0; i < 2; i++ {
				for j := 0; j < 2; j++ {
					ap[i][j] += ab * g[i][j]
				}
			}
			hox, hoy = ox, oy
		} else if gainOf != nil {
			since := math.Inf(1)
			if k > 0 {
				since = (o.Time - st.since) / rate
			}
			g := gainOf(st.area.W, since)
			av = [2][2]float64{{g, 0}, {0, g}}
		}
		// intended pen point under the player's natural mapping
		qx, qy := tx-ax, ty-ay
		ix := ax + pl.Offset[0] + hox + qx + ap[0][0]*qx + ap[0][1]*qy
		iy := ay + pl.Offset[1] + hoy + qy + ap[1][0]*qx + ap[1][1]*qy
		// vector gain relative to where the pen is
		vx, vy := ix-penX, iy-penY
		if pl.Kappa != 0 {
			d := pl.Kappa * (PivotAngle(pl.Pivot, penX+vx/2, penY+vy/2) - pl.Phi0)
			vx, vy = vx*math.Cos(d)-vy*math.Sin(d), vx*math.Sin(d)+vy*math.Cos(d)
		}
		cx := penX + vx + av[0][0]*vx + av[0][1]*vy + pl.HabitG[0]*vx
		cy := penY + vy + av[1][0]*vx + av[1][1]*vy + pl.HabitG[1]*vy
		if pl.Lazy > 0 {
			tpx, tpy := iv.toPx(tx, ty)
			ppx, ppy := iv.toPx(penX, penY)
			dx, dy := tpx-ppx, tpy-ppy
			if dl := math.Hypot(dx, dy); dl > 2*d.Radius {
				mx, my := iv.vecMM(pl.Lazy*d.Radius*dx/dl, pl.Lazy*d.Radius*dy/dl)
				cx -= mx
				cy -= my
			}
		}
		if (pl.Corner[0] != 0 || pl.Corner[1] != 0) && sp.X > 0.62*512 && sp.Y > 0.62*384 {
			cx += pl.Corner[0]
			cy += pl.Corner[1]
		}
		// noise, depending on the required movement
		rx, ry := tx-penX, ty-penY
		D := math.Hypot(rx, ry)
		clickT := o.Time + pl.gauss()*pl.TimingSD
		if clickT <= t+20 {
			clickT = t + 20
		}
		dtReal := (o.Time - t) / rate
		if dtReal < 1 {
			dtReal = 1
		}
		V := D / dtReal
		vp := math.Pow(V, pl.P)
		aPar, aPerp := pl.APar, pl.APerp
		if pl.ShakeRef > 0 && st.area.W > 0 {
			f := math.Pow(pl.ShakeRef/st.area.W, pl.ShakeExp)
			aPar, aPerp = aPar*f, aPerp*f
		}
		sPar := math.Sqrt(aPar*aPar + pl.BPar*pl.BPar*D*D + pl.CPar*pl.CPar*vp*vp)
		sPerp := math.Sqrt(aPerp*aPerp + pl.BPerp*pl.BPerp*D*D + pl.CPerp*pl.CPerp*vp*vp)
		if pl.PlaceK > 0 {
			dx, dy := st.area.X-pl.PlaceRef[0], st.area.Y-pl.PlaceRef[1]
			f := math.Exp(pl.PlaceK * (dx*dx + dy*dy))
			sPar, sPerp = sPar*f, sPerp*f
		}
		ux, uy := 1.0, 0.0
		if D > 1.5 {
			ux, uy = rx/D, ry/D
		}
		np, nq := pl.gauss()*sPar, pl.gauss()*sPerp
		cx += np*ux - nq*uy
		cy += np*uy + nq*ux
		moveTo(t, clickT, penX, penY, cx, cy)
		if releaseAt > 0 && clickT >= releaseAt {
			held = 0
			releaseAt = -1
		}
		press := pl.Rng.Float64() >= pl.MissRate
		var key int32
		if press {
			if keyAlt {
				key = osu.KeyK2 | osu.KeyM2
			} else {
				key = osu.KeyK1 | osu.KeyM1
			}
			keyAlt = !keyAlt
			// make sure the key was up before
			if held&key != 0 {
				addFrame(clickT-1, cx, cy, held&^key)
			}
		}
		held = (held &^ (osu.KeyK1 | osu.KeyM1 | osu.KeyK2 | osu.KeyM2)) | key
		addFrame(clickT, cx, cy, held)
		penX, penY = cx, cy
		t = clickT
		if o.Kind == osu.KindSlider && o.EndTime > o.Time {
			for ft := clickT + 16; ft < o.EndTime; ft += 16 {
				p := o.PathPositionAt(ft)
				ox := p.X - o.Pos.X
				oy := p.Y - o.Pos.Y
				mx, my := iv.vecMM(ox, oy)
				addFrame(ft, cx+mx, cy+my, held)
			}
			p := o.PathPositionAt(o.EndTime)
			mx, my := iv.vecMM(p.X-o.Pos.X, p.Y-o.Pos.Y)
			penX, penY = cx+mx, cy+my
			addFrame(o.EndTime, penX, penY, held)
			held = 0
			t = o.EndTime
		} else {
			releaseAt = clickT + 45
		}
	}
	addFrame(t+400, penX, penY, 0)
	return frames
}
