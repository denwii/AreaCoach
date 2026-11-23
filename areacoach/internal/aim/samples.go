package aim

import (
	"areacoach/internal/geom"
	"areacoach/internal/otd"
)

// Samples turns the pressed clicks into samples for the fit. su describes
// the game window and OpenTabletDriver's display; areaAt is the area in use
// when click i was pressed.
func Samples(cs []Click, su geom.Setup, areaAt func(i int) otd.Rect) []Sample {
	out := make([]Sample, 0, len(cs))
	var last otd.Rect
	var m geom.Mapper
	for i, c := range cs {
		if !c.Pressed || c.Radius <= 0 {
			continue
		}
		if a := areaAt(i); !m.OK() || !a.Same(last) {
			s := su
			s.Area = a
			m, last = s.Mapper(), a
			if !m.OK() {
				continue
			}
		}
		var x Sample
		x.Pen[0], x.Pen[1] = m.Point(c.Cursor.X, c.Cursor.Y)
		x.Screen[0], x.Screen[1] = su.OsuToScreen(c.Target.X, c.Target.Y)
		x.ToPx = m.Inverse()
		x.Err = [2]float64{c.Cursor.X - c.Target.X, c.Cursor.Y - c.Target.Y}
		x.Radius = c.Radius
		out = append(out, x)
	}
	return out
}
