package aim

import "areacoach/internal/osu"

// Move is how a note was reached: from where the note before ended, and
// with how much time.
type Move struct {
	From osu.Vec // end of the note before (osu!pixels)
	Gap  float64 // real ms from the end of the note before to this one
	OK   bool    // false at the start, after a spinner or a break
}

// Moves gives the move to each click; objs are the notes the clicks were
// judged on, rate the speed the map was played at.
func Moves(objs []osu.HitObject, d osu.Difficulty, rate float64, cs []Click) []Move {
	if rate <= 0 {
		rate = 1
	}
	out := make([]Move, len(cs))
	for i, c := range cs {
		if c.Obj <= 0 || c.Obj >= len(objs) {
			continue
		}
		p := &objs[c.Obj-1]
		if p.Kind == osu.KindSpinner {
			continue
		}
		gap := (c.T - p.EndTime) / rate
		if gap <= 0 || gap > 1500 {
			continue
		}
		out[i] = Move{From: p.StackedEndPos(d.StackOffset), Gap: gap, OK: true}
	}
	return out
}
