// Package aim judges clicks and finds the area that puts the most of them
// inside the circles.
package aim

import (
	"math"

	"areacoach/internal/osu"
)

// Press is a key (or pen tip) going down: map time in ms and cursor
// position in osu!pixels.
type Press struct {
	T    float64
	X, Y float64
}

// Click is one circle or slider head and the press that went for it.
type Click struct {
	Obj    int     // index of the object in the map
	T      float64 // object time (map ms)
	Target osu.Vec // where the circle was (osu!pixels, stacking and HR applied)
	Radius float64 // circle radius (osu!pixels)
	// the press: Pressed false when there was none in the hit window
	Pressed bool
	Cursor  osu.Vec // where the cursor was at the press
	PressT  float64 // map time of the press
	Hit     bool    // the press was inside the circle
}

// Dist is how far the press was from the centre, in radii (NaN without a
// press).
func (c Click) Dist() float64 {
	if !c.Pressed || c.Radius <= 0 {
		return math.NaN()
	}
	return c.Cursor.Dist(c.Target) / c.Radius
}

// KeyPresses are the presses saved in a replay: a key or a mouse button
// going down. Keys come with their mouse bit set (as osu! saves them), a
// mouse button alone without it.
func KeyPresses(frames []osu.Frame) []Press {
	var out []Press
	var prev int32
	inputs := func(k int32) [4]bool {
		return [4]bool{k&osu.KeyK1 != 0, k&osu.KeyM1 != 0 && k&osu.KeyK1 == 0, k&osu.KeyK2 != 0, k&osu.KeyM2 != 0 && k&osu.KeyK2 == 0}
	}
	for _, f := range frames {
		now, before := inputs(f.Keys), inputs(prev)
		for i := range now {
			if now[i] && !before[i] {
				out = append(out, Press{f.T, float64(f.X), float64(f.Y)})
			}
		}
		prev = f.Keys
	}
	return out
}

// Judge pairs presses with circles the way osu! does: a press goes to the
// first circle not yet hit whose hit window it is in (notelock); inside the
// circle it hits, outside it is an aim error on that circle, unless it is a
// hit on one of the next circles (then it was meant for that one).
// Presses must be in time order.
func Judge(objs []osu.HitObject, d osu.Difficulty, presses []Press) []Click {
	type target struct {
		click    Click
		attempts []Press
	}
	var ts []target
	for i := range objs {
		o := &objs[i]
		if o.Kind == osu.KindSpinner {
			continue
		}
		ts = append(ts, target{click: Click{Obj: i, T: o.Time, Target: o.StackedPos(d.StackOffset), Radius: d.Radius}})
	}
	w := d.Window50
	idx := 0
	for _, p := range presses {
		for idx < len(ts) && p.T > ts[idx].click.T+w {
			idx++
		}
		if idx >= len(ts) {
			break
		}
		tg := &ts[idx]
		if p.T-tg.click.T < -w {
			continue // too early: nothing happens
		}
		if math.Hypot(p.X-tg.click.Target.X, p.Y-tg.click.Target.Y) <= d.Radius {
			c := &tg.click
			c.Pressed, c.Hit, c.Cursor, c.PressT = true, true, osu.Vec{X: p.X, Y: p.Y}, p.T
			idx++
			continue
		}
		next := false
		for j := idx + 1; j < len(ts) && j <= idx+3; j++ {
			n := &ts[j].click
			if math.Abs(p.T-n.T) <= w && math.Hypot(p.X-n.Target.X, p.Y-n.Target.Y) <= d.Radius {
				next = true
				break
			}
		}
		if !next {
			tg.attempts = append(tg.attempts, p)
		}
	}
	out := make([]Click, len(ts))
	for i := range ts {
		c := ts[i].click
		if !c.Pressed && len(ts[i].attempts) > 0 {
			// the attempt closest in time is the one aimed at it
			b := ts[i].attempts[0]
			for _, a := range ts[i].attempts[1:] {
				if math.Abs(a.T-c.T) < math.Abs(b.T-c.T) {
					b = a
				}
			}
			c.Pressed, c.Cursor, c.PressT = true, osu.Vec{X: b.X, Y: b.Y}, b.T
		}
		out[i] = c
	}
	return out
}

// A map given up halfway: the pen lifted (or pressing without aiming) until
// the end. Those notes say nothing about the area.
const (
	giveUpFar   = 3.0 // a press this many radii away is not an attempt
	giveUpMin   = 5
	giveUpShare = 0.85
	giveUpHits  = 20
)

func notPlayed(c *Click) bool {
	if !c.Pressed {
		return true
	}
	return !c.Hit && c.Dist() > giveUpFar
}

// Played is how many clicks, from the start, were really played: all of
// them unless the map ends with a stretch given up.
func Played(cs []Click) int {
	n := len(cs)
	if n < giveUpMin {
		return n
	}
	best := n
	idle, hit := 0, 0
	for g := n - 1; g >= 0; g-- {
		c := &cs[g]
		if notPlayed(c) {
			idle++
		} else if c.Hit {
			hit++
		}
		k := n - g
		if k >= giveUpMin && notPlayed(c) && float64(idle) >= giveUpShare*float64(k) && hit*giveUpHits <= k {
			best = g
		}
	}
	return best
}
