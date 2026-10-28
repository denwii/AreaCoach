package analysis

import (
	"fmt"
	"math"
	"strings"

	"areacoach/internal/otd"
)

// Phases of the coaching cycle: learn how the player aims, set the
// recommended area, try it, keep it.
const (
	PhaseDiagnosis = iota
	PhaseSet
	PhaseTest
	PhaseMaintain
)

var phaseKeys = []string{"learn", "set", "trial", "keep"}

// num formats a number with d decimals.
func num(x float64, d int) string { return fmt.Sprintf("%.*f", d, x) }

func numAbs(x float64, d int) string { return num(math.Abs(x), d) }

func pct(x float64, d int) string { return num(100*x, d) + "%" }

func moveText(dx, dy float64) string {
	ax, ay := math.Abs(dx), math.Abs(dy)
	h, v := "right", "down"
	if dx < 0 {
		h = "left"
	}
	if dy < 0 {
		v = "up"
	}
	var parts []string
	if ax >= 0.1 {
		parts = append(parts, numAbs(dx, 1)+" mm "+h)
	}
	if ay >= 0.1 {
		parts = append(parts, numAbs(dy, 1)+" mm "+v)
	}
	if len(parts) == 0 {
		return "less than 0.1 mm"
	}
	return strings.Join(parts, " and ")
}

func buildHistory(epochs []EpochInfo) []HistItem {
	var out []HistItem
	for i := 0; i < len(epochs); i++ {
		e := epochs[i]
		if i == 0 {
			txt := fmt.Sprintf("First area recorded: %s × %s mm.", num(e.Setup.Area.W, 1), num(e.Setup.Area.H, 1))
			out = append(out, HistItem{Date: e.From, Text: txt, Kind: "start"})
			continue
		}
		// the areas of an automatic calibration: one line for all of them
		if e.Source == "calibrazione" {
			j := i
			lo, hi := math.Inf(1), math.Inf(-1)
			seen := map[[3]float64]bool{}
			for j < len(epochs) && epochs[j].Source == "calibrazione" {
				a := epochs[j].Setup.Area
				lo, hi = math.Min(lo, a.W), math.Max(hi, a.W)
				seen[[3]float64{math.Round(a.W * 10), math.Round(a.X * 10), math.Round(a.Y * 10)}] = true
				j++
			}
			last := epochs[j-1].Setup.Area
			txt := fmt.Sprintf("In-game diagnosis: areas between %s and %s mm wide; in the end %s × %s mm.", num(lo, 1), num(hi, 1), num(last.W, 1), num(last.H, 1))
			if epochs[i-1].Setup.Area.Same(last) {
				txt = strings.TrimSuffix(txt, ".") + ", the previous area again."
			}
			out = append(out, HistItem{Date: e.From, Text: txt, Kind: "calib"})
			i = j - 1
			continue
		}
		p := epochs[i-1].Setup.Area
		a := e.Setup.Area
		var bits []string
		if math.Hypot(a.X-p.X, a.Y-p.Y) >= 0.05 {
			bits = append(bits, "moved "+moveText(a.X-p.X, a.Y-p.Y))
		}
		if math.Abs(a.W-p.W) >= 0.05 || math.Abs(a.H-p.H) >= 0.05 {
			bits = append(bits, fmt.Sprintf("from %s × %s to %s × %s mm", num(p.W, 1), num(p.H, 1), num(a.W, 1), num(a.H, 1)))
		}
		if math.Abs(a.Rot-p.Rot) >= 0.05 {
			bits = append(bits, fmt.Sprintf("rotation from %s° to %s°", num(p.Rot, 1), num(a.Rot, 1)))
		}
		if e.Setup.SmoothingSet && epochs[i-1].Setup.SmoothingSet && e.Setup.Smoothing != epochs[i-1].Setup.Smoothing {
			if e.Setup.Smoothing == "" {
				bits = append(bits, "OpenTabletDriver filters turned off")
			} else {
				bits = append(bits, "OpenTabletDriver filters changed")
			}
		}
		if len(bits) == 0 {
			continue
		}
		kind := "manual"
		lead := "You changed the area: "
		if e.PrevAdvice != nil && math.Abs(e.PrevAdvice.W-a.W) < 0.35 && math.Abs(e.PrevAdvice.H-a.H) < 0.35 &&
			math.Hypot(e.PrevAdvice.X-a.X, e.PrevAdvice.Y-a.Y) < 0.35 && math.Abs(e.PrevAdvice.Rot-a.Rot) < 0.3 {
			kind = "advice"
			lead = "You applied the suggestion: "
		}
		out = append(out, HistItem{Date: e.From, Text: lead + strings.Join(bits, ", ") + ".", Kind: kind})
	}
	return out
}

func coach(in Input, cur *EpochInfo, epochAim int, rep *Report, cons ConsistencyOut) CoachOut {
	c := CoachOut{History: buildHistory(in.periods())}
	area := cur.Setup.Area
	a := rep.Areas
	rec := a.Rec
	if rec == nil || a.Final == nil {
		hits := cons.NoiseHits
		c.Phase, c.PhaseIndex = phaseKeys[PhaseDiagnosis], PhaseDiagnosis
		c.Progress = math.Min(0.95, float64(hits)/minDiagHits)
		c.Title = "Learning how you aim"
		if hits == 0 {
			c.Message = "Play normally: every saved replay is analysed by itself. The first suggestion needs about " +
				fmt.Sprint(minDiagHits) + " notes hit, that is 3-5 maps."
		} else {
			c.Message = fmt.Sprintf("Keep playing normally. I still need about %d notes hit for a reliable suggestion.", roundTo(minDiagHits-hits, 50))
		}
		return c
	}
	final := a.Final.Rect
	ev := recEvidence(rep, cons)
	// change the size only when it makes a difference you would notice: in
	// the predicted misses, or on the same maps played with other widths
	meaningful := rec.CurMiss < 0 || rec.FinalMiss < 0 || rec.CurMiss-rec.FinalMiss >= math.Max(0.05, 0.03*rec.CurMiss)
	if d := rec.Decision; d != nil && d.Source != "model" {
		meaningful = true
	}
	sizeDiff := (math.Abs(area.W-final.W) >= 0.8 || math.Abs(area.H-final.H) >= 0.8) && meaningful
	switch {
	case rec.Testing:
		c.Phase, c.PhaseIndex = phaseKeys[PhaseTest], PhaseTest
		c.Progress = math.Min(float64(rec.TestPlays)/float64(rec.NeedPlays), float64(rec.TestHits)/float64(rec.NeedHits))
		c.Progress = clamp(c.Progress, 0.05, 0.95)
		var need []string
		if left := rec.NeedPlays - rec.TestPlays; left > 0 {
			if left == 1 {
				need = append(need, "1 more map")
			} else {
				need = append(need, fmt.Sprintf("%d more maps", left))
			}
		}
		if left := rec.NeedHits - rec.TestHits; left > 0 {
			need = append(need, fmt.Sprintf("about %d notes", roundTo(left, 50)))
		}
		c.Title = "You're trying the suggested area"
		c.Message = fmt.Sprintf("Play normally with this area: before judging it again I need %s. Until then the suggestion stays put, so it doesn't change after every map and the comparison with the other areas is fair.", joinAnd(need))
	case sizeDiff:
		c.Phase, c.PhaseIndex = phaseKeys[PhaseSet], PhaseSet
		c.Title = fmt.Sprintf("Set the suggested area: %s × %s mm", num(final.W, 1), num(final.H, 1))
		msg := "According to all your replays, it's the width with which you miss least, adding up every kind of pattern."
		if rec.CurMiss >= 0 && rec.FinalMiss >= 0 && rec.CurMiss-rec.FinalMiss >= 0.05 {
			msg += fmt.Sprintf(" Expected aim misses: %s per 1000 notes instead of %s with the current area.", num(rec.FinalMiss, 1), num(rec.CurMiss, 1))
		}
		msg += " The suggestion updates with every replay, even right after you set it."
		c.Message = msg
		c.Next = &final
		c.Change = &Change{DX: round(final.X-area.X, 2), DY: round(final.Y-area.Y, 2), DW: round(final.W-area.W, 2), DH: round(final.H-area.H, 2), Kind: "size"}
		if rel := final.W/area.W - 1; math.Abs(rel) > 0.08 {
			mid := area.W + (final.W-area.W)/2
			ev = append([]Evidence{{Text: fmt.Sprintf("It's a big change (from %s to %s mm). If you'd rather get there in steps, set about %s mm first: the suggestion doesn't change because of it.", num(area.W, 1), num(final.W, 1), num(mid, 1)), Tone: "neutral"}}, ev...)
		}
	case !rec.Centered:
		sh := a.Shifted.Rect
		c.Phase, c.PhaseIndex = phaseKeys[PhaseSet], PhaseSet
		c.Title = "Move the area " + moveText(sh.X-area.X, sh.Y-area.Y)
		c.Message = "The size is fine, but your hand works in a slightly different spot of the tablet. Moving the area there puts the circles under the pen without having to correct."
		c.Next = &sh
		c.Change = &Change{DX: round(sh.X-area.X, 2), DY: round(sh.Y-area.Y, 2), Kind: "position"}
	default:
		c.Phase, c.PhaseIndex = phaseKeys[PhaseMaintain], PhaseMaintain
		c.Progress = 1
		if rec.SameAsFinal {
			c.Title = "Your area is the right one"
			c.Message = fmt.Sprintf("It matches the suggested area, worked out from all your replays, and the %d plays you made with it don't contradict it. Don't touch anything: I recalculate it with every replay and only tell you if the data really changes.", rec.TestPlays)
		} else {
			c.Title = "Your area is fine"
			c.Message = fmt.Sprintf("The suggested area is %s × %s mm, but the difference from yours is so small it wouldn't change the misses measurably. You can keep it: I keep checking with every replay.", num(final.W, 1), num(final.H, 1))
		}
		c.Converged = []string{"position", "size"}
	}
	c.Evidence = ev
	return c
}

// recEvidence explains the recommendation, from the most to the least
// important line.
func recEvidence(rep *Report, cons ConsistencyOut) []Evidence {
	var ev []Evidence
	rec := rep.Areas.Rec
	cr := cons.Cross
	final := rep.Areas.Final.Rect
	ev = append(ev, Evidence{Text: fmt.Sprintf("Worked out from %d replays played with %d %s: the area set now doesn't change the calculation, only new plays can.",
		rec.Plays, cr.Areas, map[bool]string{true: "area", false: "different areas"}[cr.Areas == 1]), Tone: "neutral"})
	if rec.Kept {
		ev = append(ev, Evidence{Text: "Same suggestion as last time: the new calculation is within the margin of uncertainty, so I don't move it on a fluke.", Tone: "good"})
	}
	if d := rec.Decision; d != nil {
		switch d.Source {
		case "combined":
			ev = append(ev, Evidence{Text: fmt.Sprintf("On the maps you replayed with different areas you miss least around %s mm (between %s and %s): I combined it with the model's prediction, which said %s mm.", num(d.EmpW, 1), num(d.EmpLo, 1), num(d.EmpHi, 1), num(d.ModelW, 1)), Tone: "good"})
		case "evidence":
			ev = append(ev, Evidence{Text: fmt.Sprintf("The model would point to about %s mm, but on the same maps with that width you missed %s more than with %s mm (%s). I follow the real data.", num(d.ModelW, 1), pct(d.EvidRel, 0), num(d.EvidW, 1), mapsText(d.EvidMaps)), Tone: "good"})
		case "evidence-step":
			ev = append(ev, Evidence{Text: fmt.Sprintf("The model would point to about %s mm, but on the same maps the widths you tried in that direction did worse than %s mm (%s). I get closer by one step only, to check.", num(d.ModelW, 1), num(d.EvidW, 1), mapsText(d.EvidMaps)), Tone: "neutral"})
		case "confirmed":
			ev = append(ev, Evidence{Text: fmt.Sprintf("Confirmed by the real data: on the same maps, %s mm is the width with which you missed least (%s).", num(d.EvidW, 1), mapsText(d.EvidMaps)), Tone: "good"})
		}
	}
	// every kind of pattern wants its own width: the recommendation balances them
	var parts []string
	for _, k := range rec.ByCat {
		if k.Weight < 0.03 || k.BestW <= 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s mm", strings.ToLower(k.Name), num(k.BestW, 1)))
	}
	agree := true
	for _, k := range rec.ByCat {
		if k.Weight >= 0.03 && k.BestW > 0 && math.Abs(k.BestW-final.W) > 1 {
			agree = false
		}
	}
	byEvidence := rec.Decision != nil && strings.HasPrefix(rec.Decision.Source, "evidence")
	switch {
	case len(parts) >= 2 && byEvidence:
		ev = append(ev, Evidence{Text: fmt.Sprintf("According to the precision model: %s. The real comparisons on the same maps say otherwise, though, and they count more than a prediction.", joinAnd(parts)), Tone: "neutral"})
	case len(parts) >= 2 && !agree:
		ev = append(ev, Evidence{Text: fmt.Sprintf("Each kind of pattern would want a different width: %s. The suggested one (%s mm) has the fewest misses in total, weighted by how often each kind shows up in the maps you play.", joinAnd(parts), num(final.W, 1)), Tone: "neutral"})
	case len(parts) >= 2:
		ev = append(ev, Evidence{Text: "Every kind of pattern you play agrees on this width.", Tone: "good"})
	}
	// circle sizes: worth a line only when they want different widths
	var csParts []string
	csLo, csHi := math.Inf(1), math.Inf(-1)
	for _, b := range cons.ByCS {
		if b.BestW > 0 {
			csParts = append(csParts, fmt.Sprintf("%s → %s mm (%s of the notes)", b.Name, num(b.BestW, 1), pct(b.Share, 0)))
			csLo, csHi = math.Min(csLo, b.BestW), math.Max(csHi, b.BestW)
		}
	}
	if len(csParts) >= 2 && csHi-csLo > 1 {
		pre := "By circle size: "
		if byEvidence {
			pre = "By circle size, according to the model: "
		}
		ev = append(ev, Evidence{Text: pre + strings.Join(csParts, "; ") + ".", Tone: "neutral"})
	}
	if cr.PersistKnown && cr.NaturalKnown {
		ev = append(ev, Evidence{Text: fmt.Sprintf("From the areas you used: your hand naturally works on about %s mm, and even once used to an area %s of the difference from it remains. That's why the suggestion doesn't chase the last area used.", num(cr.NaturalW, 1), pct(cr.Persist, 0)), Tone: "neutral"})
	}
	switch rec.Edge {
	case "up":
		ev = append(ev, Evidence{Text: fmt.Sprintf("You haven't played enough with areas above %s mm yet: to be safe I widen by at most 6%% beyond the ones tried. If you do better with this one, the next suggestion can widen further.", num(rec.ExploredHi, 1)), Tone: "neutral"})
	case "down":
		ev = append(ev, Evidence{Text: fmt.Sprintf("You haven't played enough with areas below %s mm yet: to be safe I narrow by at most 6%% beyond the ones tried. If you do better with this one, the next suggestion can narrow further.", num(rec.ExploredLo, 1)), Tone: "neutral"})
	}
	if rec.SpeedCapped {
		ev = append(ev, Evidence{Text: fmt.Sprintf("With areas above %s mm you would have to move the pen faster than you ever have: predictions there aren't reliable and I don't use them.", num(cons.TrustHiW, 1)), Tone: "neutral"})
	}
	// circle sizes compared on the same maps
	var same []string
	for i := range cr.Effects {
		e := &cr.Effects[i]
		if e.Group != "cs" || e.Verdict == "unclear" {
			continue
		}
		if cb := cr.Combined; cb != nil && cb.Verdict == e.Verdict && math.Abs(cb.BestW-e.BestW) < 1 {
			same = append(same, strings.TrimPrefix(e.Name, "CS "))
			continue
		}
		ev = append(ev, Evidence{Text: fmt.Sprintf("Same maps, %s: %s.", e.Name, curveText(e)), Tone: "neutral"})
	}
	if cb := cr.Combined; cb != nil && (rec.Decision == nil || rec.Decision.Source == "model" || rec.Decision.Source == "combined") {
		if t := curveText(cb); t != "" {
			txt := "On the maps replayed with different areas " + t
			if len(same) > 0 {
				txt += fmt.Sprintf(", and it holds for every circle size compared (CS %s)", strings.Join(same, "; "))
			}
			ev = append(ev, Evidence{Text: txt + ".", Tone: "neutral"})
		}
	}
	return ev
}

func mapsText(n int) string {
	if n == 1 {
		return "1 map"
	}
	return fmt.Sprintf("%d maps", n)
}

// curveText describes a same-map width curve in a few words.
func curveText(e *SameMapEffect) string {
	maps := mapsText(e.Maps)
	switch e.Verdict {
	case "bigger", "smaller":
		return fmt.Sprintf("you miss least with about %s mm: error %s against %s mm (%s)", num(e.BestW, 1), signPct(e.Gain), num(e.RefW, 1), maps)
	case "current":
		return fmt.Sprintf("%s mm is the best of the widths tried (%s)", num(e.RefW, 1), maps)
	}
	if e.Maps >= 2 {
		return fmt.Sprintf("no sure difference yet between the widths tried, from %s to %s mm (%s)", num(e.WidthMin, 1), num(e.WidthMax, 1), maps)
	}
	return ""
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func signPct(x float64) string {
	if x >= 0 {
		return "+" + pct(x, 0)
	}
	return "−" + pct(-x, 0)
}

func roundTo(n, step int) int {
	if n <= step {
		return step
	}
	return (n + step/2) / step * step
}

func insights(cons ConsistencyOut, nf NoiseFit, areas AreasOut, cur otd.Rect) []string {
	var out []string
	if !cons.Ready || areas.Rec == nil || areas.Final == nil {
		return out
	}
	rec := areas.Rec
	m := nf.Model
	out = append(out, fmt.Sprintf("Even in short movements the pen wobbles by about %s mm around the intended point. That's the part of the error a bigger area reduces.", num(m.APar, 2)))
	base, _ := m.Sigma(40, 0)
	full, _ := m.Sigma(40, m.VMax)
	if m.VMax > 0 && full > base*1.25 {
		v := math.Max(0.1, m.VMax*0.6)
		if m.P > 1.25 {
			out = append(out, fmt.Sprintf("Beyond about %s m/s your precision drops more than in proportion to speed: that's why an area that is too big hurts you on fast jumps.", num(v, 2)))
		} else {
			out = append(out, "Your precision drops steadily with speed: I see no point in the data beyond which it collapses.")
		}
	} else {
		out = append(out, "At the speeds you play, precision hardly depends on speed: the area can grow without costing you on fast jumps, as long as you stay within the range of your movements.")
	}
	fw := areas.Final.W
	var bigger, smaller, same []string
	for _, k := range rec.ByCat {
		if k.BestW <= 0 {
			continue
		}
		name := strings.ToLower(k.Name)
		switch {
		case k.BestW >= fw*1.02:
			bigger = append(bigger, fmt.Sprintf("%s (%s mm)", name, num(k.BestW, 1)))
		case k.BestW <= fw*0.98:
			smaller = append(smaller, fmt.Sprintf("%s (%s mm)", name, num(k.BestW, 1)))
		default:
			same = append(same, name)
		}
	}
	if len(bigger) > 0 {
		out = append(out, fmt.Sprintf("On their own, these would prefer an area bigger than the suggested one: %s.", joinAnd(bigger)))
	}
	if len(smaller) > 0 {
		out = append(out, fmt.Sprintf("On their own, these would prefer an area smaller than the suggested one: %s.", joinAnd(smaller)))
	}
	if len(same) > 0 {
		out = append(out, fmt.Sprintf("For %s the suggested one is already the best width.", joinAnd(same)))
	}
	if len(rec.Tried) > 0 {
		out = append(out, fmt.Sprintf("Widths tried enough to count: from %s to %s mm. The suggestion can go beyond this range by at most 6%%: so it only widens or narrows after you have really played with the nearby areas.", num(rec.ExploredLo, 1), num(rec.ExploredHi, 1)))
	}
	return out
}
