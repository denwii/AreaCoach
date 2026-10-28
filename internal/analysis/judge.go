package analysis

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"

	"areacoach/internal/osu"
)

// Hit results.
const (
	R300 = iota
	R100
	R50
	RAimMiss // pressed in time but outside the circle
	RNoPress // no press in the hit window
)

// Hit is the aim measurement for one circle or slider head.
// Positions are osu!pixels in screen orientation (HR already applied).
type Hit struct {
	Obj    int32
	Time   float32 // map ms
	Kind   uint8   // 0 circle, 1 slider head
	Result uint8
	TX, TY float32 // stacked target
	CX, CY float32 // cursor at the press (NaN if none)
	CT     float32 // press time - object time (map ms, NaN if none)
	OX, OY float32 // cursor where the movement started (NaN after breaks)
	PX, PY float32 // previous object's end on the map (NaN after breaks)
	Dt     float32 // real ms available for the movement (NaN after breaks)
	Radius float32
	Over   float32 // furthest point past the target along the movement (px), NaN if n/a
	Settle float32 // cursor path in the last 50 ms before the press (px), NaN if n/a
}

// HasClick reports whether the hit has a press position.
func (h *Hit) HasClick() bool { return !math.IsNaN(float64(h.CX)) }

// HasOrigin reports whether the movement origin is known.
func (h *Hit) HasOrigin() bool { return !math.IsNaN(float64(h.OX)) }

// Hit encoding: 17 little-endian float32 per hit.
const hitFields = 17

// EncodeHits serialises hits compactly.
func EncodeHits(hs []Hit) []byte {
	out := make([]byte, 0, 8+len(hs)*hitFields*4)
	out = binary.LittleEndian.AppendUint32(out, 0x41434831) // "ACH1"
	out = binary.LittleEndian.AppendUint32(out, uint32(len(hs)))
	for _, h := range hs {
		vals := [hitFields]float32{float32(h.Obj), h.Time, float32(h.Kind), float32(h.Result), h.TX, h.TY, h.CX, h.CY, h.CT,
			h.OX, h.OY, h.PX, h.PY, h.Dt, h.Radius, h.Over, h.Settle}
		for _, v := range vals {
			out = binary.LittleEndian.AppendUint32(out, math.Float32bits(v))
		}
	}
	return out
}

// DecodeHits is the inverse of EncodeHits.
func DecodeHits(b []byte) ([]Hit, error) {
	if len(b) < 8 || binary.LittleEndian.Uint32(b) != 0x41434831 {
		return nil, errors.New("invalid hit data")
	}
	n := int(binary.LittleEndian.Uint32(b[4:]))
	if len(b) < 8+n*hitFields*4 {
		return nil, errors.New("truncated hit data")
	}
	hs := make([]Hit, n)
	p := 8
	f := func() float32 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[p:]))
		p += 4
		return v
	}
	for i := range hs {
		h := &hs[i]
		h.Obj = int32(f())
		h.Time = f()
		h.Kind = uint8(f())
		h.Result = uint8(f())
		h.TX, h.TY, h.CX, h.CY, h.CT = f(), f(), f(), f(), f()
		h.OX, h.OY, h.PX, h.PY, h.Dt = f(), f(), f(), f(), f()
		h.Radius, h.Over, h.Settle = f(), f(), f()
	}
	return hs, nil
}

var nan32 = float32(math.NaN())

type press struct {
	t    float64
	x, y float64
}

type cursor struct{ frames []osu.Frame }

func (c cursor) at(t float64) (float64, float64) {
	fs := c.frames
	n := len(fs)
	if n == 0 {
		return math.NaN(), math.NaN()
	}
	i := sort.Search(n, func(i int) bool { return fs[i].T >= t })
	if i == 0 {
		return float64(fs[0].X), float64(fs[0].Y)
	}
	if i >= n {
		return float64(fs[n-1].X), float64(fs[n-1].Y)
	}
	a, b := fs[i-1], fs[i]
	if b.T <= a.T {
		return float64(b.X), float64(b.Y)
	}
	f := (t - a.T) / (b.T - a.T)
	return float64(a.X) + (float64(b.X)-float64(a.X))*f, float64(a.Y) + (float64(b.Y)-float64(a.Y))*f
}

// framesBetween returns the indices of frames with t0 < T <= t1.
func (c cursor) framesBetween(t0, t1 float64) (int, int) {
	fs := c.frames
	i := sort.Search(len(fs), func(i int) bool { return fs[i].T > t0 })
	j := sort.Search(len(fs), func(i int) bool { return fs[i].T > t1 })
	return i, j
}

// PlaySummary aggregates a judged play.
type PlaySummary struct {
	Objects  int     `json:"objects"`
	Heads    int     `json:"heads"`
	N300     int     `json:"n300"`
	N100     int     `json:"n100"`
	N50      int     `json:"n50"`
	AimMiss  int     `json:"aimMiss"`
	NoPress  int     `json:"noPress"`
	Presses  int     `json:"presses"`
	LastTime float64 `json:"lastTime"`
}

// Judge simulates stable's hit detection (notelock, hit windows, circle
// radius) and measures aim for every circle and slider head.
func Judge(objs []osu.HitObject, d osu.Difficulty, frames []osu.Frame) ([]Hit, PlaySummary) {
	cur := cursor{frames: frames}
	var sum PlaySummary
	sum.Objects = len(objs)
	// presses
	var presses []press
	// a press is a key or a mouse button going down: the keys K1/K2 come
	// with the M1/M2 bit (as osu! saves them), a mouse button alone without
	// it; so a key pressed while a mouse button is held is still a press
	var prevKeys int32
	inputs := func(k int32) [4]bool {
		return [4]bool{k&osu.KeyK1 != 0, k&osu.KeyM1 != 0 && k&osu.KeyK1 == 0, k&osu.KeyK2 != 0, k&osu.KeyM2 != 0 && k&osu.KeyK2 == 0}
	}
	for _, f := range frames {
		now, before := inputs(f.Keys), inputs(prevKeys)
		for i := range now {
			if now[i] && !before[i] {
				presses = append(presses, press{f.T, float64(f.X), float64(f.Y)})
			}
		}
		prevKeys = f.Keys
	}
	sum.Presses = len(presses)
	if len(frames) > 0 {
		sum.LastTime = frames[len(frames)-1].T
	}

	type target struct {
		oi       int
		t        float64
		pos      osu.Vec
		kind     uint8
		hit      bool
		res      uint8
		click    press
		attempts []press
	}
	var targets []target
	for i := range objs {
		o := &objs[i]
		if o.Kind == osu.KindSpinner {
			continue
		}
		k := uint8(0)
		if o.Kind == osu.KindSlider {
			k = 1
		}
		targets = append(targets, target{oi: i, t: o.Time, pos: o.StackedPos(d.StackOffset), kind: k})
	}
	sum.Heads = len(targets)
	w50 := d.Window50
	idx := 0
	for _, p := range presses {
		for idx < len(targets) && p.t > targets[idx].t+w50 {
			idx++
		}
		if idx >= len(targets) {
			break
		}
		tg := &targets[idx]
		dt := p.t - tg.t
		if dt < -w50 {
			continue // too early: the circle shakes, nothing happens
		}
		if math.Hypot(p.x-tg.pos.X, p.y-tg.pos.Y) <= d.Radius {
			tg.hit = true
			tg.click = p
			adt := math.Abs(dt)
			switch {
			case adt <= d.Window300:
				tg.res = R300
			case adt <= d.Window100:
				tg.res = R100
			default:
				tg.res = R50
			}
			idx++
		} else {
			// a press on a later circle is meant for that one (notelock),
			// not an aim error on this one
			meantForNext := false
			for j := idx + 1; j < len(targets) && j <= idx+3; j++ {
				nt := &targets[j]
				if math.Abs(p.t-nt.t) <= w50 && math.Hypot(p.x-nt.pos.X, p.y-nt.pos.Y) <= d.Radius {
					meantForNext = true
					break
				}
			}
			if !meantForNext {
				tg.attempts = append(tg.attempts, p)
			}
		}
	}

	hits := make([]Hit, 0, len(targets))
	for ti := range targets {
		tg := &targets[ti]
		o := &objs[tg.oi]
		h := Hit{Obj: int32(tg.oi), Time: float32(tg.t), Kind: tg.kind, TX: float32(tg.pos.X), TY: float32(tg.pos.Y),
			CX: nan32, CY: nan32, CT: nan32, OX: nan32, OY: nan32, PX: nan32, PY: nan32, Dt: nan32,
			Radius: float32(d.Radius), Over: nan32, Settle: nan32}
		var click *press
		if tg.hit {
			h.Result = tg.res
			click = &tg.click
		} else if len(tg.attempts) > 0 {
			h.Result = RAimMiss
			best := 0
			for k := range tg.attempts {
				if math.Abs(tg.attempts[k].t-tg.t) < math.Abs(tg.attempts[best].t-tg.t) {
					best = k
				}
			}
			click = &tg.attempts[best]
		} else {
			h.Result = RNoPress
		}
		switch h.Result {
		case R300:
			sum.N300++
		case R100:
			sum.N100++
		case R50:
			sum.N50++
		case RAimMiss:
			sum.AimMiss++
		case RNoPress:
			sum.NoPress++
		}
		if click != nil {
			h.CX, h.CY = float32(click.x), float32(click.y)
			h.CT = float32(click.t - tg.t)
		}
		// previous object (not a spinner) and movement origin
		if tg.oi > 0 {
			po := &objs[tg.oi-1]
			if po.Kind != osu.KindSpinner {
				var prevEnd osu.Vec
				var te, teCursor float64
				if po.Kind == osu.KindSlider {
					prevEnd = po.StackedEndPos(d.StackOffset)
					te = po.EndTime
					teCursor = math.Max(po.EndTime-36, po.Time)
				} else {
					prevEnd = po.StackedPos(d.StackOffset)
					te = po.Time
					teCursor = po.Time
					if ti > 0 && targets[ti-1].oi == tg.oi-1 && targets[ti-1].hit {
						teCursor = targets[ti-1].click.t
					}
				}
				dtReal := (tg.t - te) / d.Rate
				if dtReal > 0 && dtReal < 1500 {
					h.PX, h.PY = float32(prevEnd.X), float32(prevEnd.Y)
					h.Dt = float32(dtReal)
					ox, oy := cur.at(teCursor)
					if !math.IsNaN(ox) {
						h.OX, h.OY = float32(ox), float32(oy)
					}
					if click != nil && !math.IsNaN(ox) {
						mx, my := tg.pos.X-ox, tg.pos.Y-oy
						ml := math.Hypot(mx, my)
						if ml > 2*d.Radius {
							ux, uy := mx/ml, my/ml
							i0, i1 := cur.framesBetween(teCursor, click.t)
							best := math.Inf(-1)
							for fi := i0; fi < i1; fi++ {
								f := frames[fi]
								pr := (float64(f.X)-tg.pos.X)*ux + (float64(f.Y)-tg.pos.Y)*uy
								if pr > best {
									best = pr
								}
							}
							pr := (click.x-tg.pos.X)*ux + (click.y-tg.pos.Y)*uy
							if pr > best {
								best = pr
							}
							h.Over = float32(best)
						}
					}
				}
			}
		}
		if click != nil {
			i0, i1 := cur.framesBetween(click.t-50*d.Rate, click.t)
			if i1-i0 >= 1 {
				px, py := cur.at(click.t - 50*d.Rate)
				l := 0.0
				for fi := i0; fi < i1; fi++ {
					f := frames[fi]
					l += math.Hypot(float64(f.X)-px, float64(f.Y)-py)
					px, py = float64(f.X), float64(f.Y)
				}
				h.Settle = float32(l)
			}
		}
		_ = o
		hits = append(hits, h)
	}
	return hits, sum
}
