package sim

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
)

const lazerBeatmaps = "../../../ext/lazer/osu.Game.Rulesets.Osu.Tests/Resources/Testing/Beatmaps"

func testSetup() analysis.Setup {
	return analysis.Setup{TabletName: "Wacom CTL-472", TabletW: 152, TabletH: 95,
		Area:    otd.Rect{W: 80, H: 45, X: 76, Y: 47.5},
		Display: otd.Rect{W: 1920, H: 1080, X: 960, Y: 540},
		GameW:   1920, GameH: 1080, StretchX: 1, StretchY: 1}
}

func truePlayer(seed int64) *Player {
	th := 1.5 * math.Pi / 180
	return &Player{
		Offset: [2]float64{1.2, -0.6},
		Av:     [2][2]float64{{0.04, 0}, {0, 0.04}},
		Ap:     [2][2]float64{{math.Cos(th) - 1, -math.Sin(th)}, {math.Sin(th), math.Cos(th) - 1}},
		APar:   0.5, BPar: 0.025, CPar: 0.6, P: 1.6,
		APerp: 0.4, BPerp: 0.015, CPerp: 0.35,
		TimingSD: 12, MissRate: 0.01,
		Rng: rand.New(rand.NewSource(seed)),
	}
}

func simulatePlays(t *testing.T, n int, mods uint32) []*analysis.PlayData {
	maps := []string{"1124896", "1341554", "2593923", "801165"}
	setup := testSetup()
	var plays []*analysis.PlayData
	base := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		bm, err := osu.ParseBeatmapFile(filepath.Join(lazerBeatmaps, maps[i%len(maps)]+".osu"))
		if err != nil {
			t.Skip("reference maps not available")
		}
		pl := truePlayer(int64(100 + i))
		frames := pl.Play(bm, mods, setup)
		objs, d := bm.Objects(mods)
		hits, sum := analysis.Judge(objs, d, frames)
		if i == 0 {
			t.Logf("map %s: heads=%d 300=%d 100=%d 50=%d aimMiss=%d noPress=%d presses=%d", maps[0], sum.Heads, sum.N300, sum.N100, sum.N50, sum.AimMiss, sum.NoPress, sum.Presses)
		}
		plays = append(plays, &analysis.PlayData{ID: i + 1, Time: base.Add(time.Duration(i) * 7 * time.Hour), Epoch: 1,
			Setup: setup, Rate: d.Rate, Hits: hits, Duration: bm.LengthMs()})
	}
	return plays
}

func TestRecoverBias(t *testing.T) {
	plays := simulatePlays(t, 12, 0)
	sessions, of := analysis.BuildSessions(plays)
	samples := analysis.BuildSamples(plays, of)
	ptrs := make([]*analysis.Sample, len(samples))
	for i := range samples {
		ptrs[i] = &samples[i]
	}
	fit := analysis.FitBias(ptrs, nil, func(s *analysis.Sample) int { return s.Play })
	if !fit.OK() {
		t.Fatal("fit failed")
	}
	set := fit.Derive(testSetup().Area)
	boot := fit.Bootstrap(testSetup().Area, 200, 1)
	t.Logf("sessions=%d samples=%d used=%d scale=%.3f", len(sessions), len(samples), fit.N, fit.Scale)
	t.Logf("offset=(%.3f, %.3f) ±(%.3f, %.3f) [true 1.2, -0.6]", fit.D[0], fit.D[1], boot.DX, boot.DY)
	t.Logf("Av=%.4f %.4f / %.4f %.4f  [true 0.04 diag]", fit.Av[0][0], fit.Av[0][1], fit.Av[1][0], fit.Av[1][1])
	t.Logf("Ap=%.4f %.4f / %.4f %.4f  [true rot 1.5°: 0 -0.026 / 0.026 0]", fit.Ap[0][0], fit.Ap[0][1], fit.Ap[1][0], fit.Ap[1][1])
	t.Logf("lambda=%.4f ±%.4f  ideal=%.2fx%.2f rot=%.2f° ±%.2f", set.Lambda, boot.Lambda, set.Ideal.W, set.Ideal.H, set.Rotation, boot.Rotation)
	if math.Abs(fit.D[0]-1.2) > 0.25 || math.Abs(fit.D[1]+0.6) > 0.25 {
		t.Errorf("offset not recovered")
	}
	if math.Abs(set.Lambda-1.04) > 0.015 {
		t.Errorf("scale not recovered: %.4f", set.Lambda)
	}
	if math.Abs(set.Rotation-1.5) > 0.8 {
		t.Errorf("rotation not recovered: %.3f", set.Rotation)
	}

	nf := analysis.FitNoise(ptrs, func(*analysis.Sample) *analysis.BiasFit { return &fit }, func(s *analysis.Sample) int { return s.Play }, 20, 2)
	m := nf.Model
	t.Logf("noise N=%d par a=%.3f b=%.4f c=%.3f | perp a=%.3f b=%.4f c=%.3f | p=%.2f vmax=%.3f", m.N, m.APar, m.BPar, m.CPar, m.APerp, m.BPerp, m.CPerp, m.P, m.VMax)
	t.Logf("true      par a=0.500 b=0.0250 c=0.600 | perp a=0.400 b=0.0150 c=0.350 | p=1.60")
	for _, dv := range [][2]float64{{2, 0.05}, {20, 0.15}, {40, 0.25}, {60, 0.5}} {
		sp, sq := m.Sigma(dv[0], dv[1])
		tp := math.Sqrt(0.25 + math.Pow(0.025*dv[0], 2) + math.Pow(0.6*math.Pow(dv[1], 1.6), 2))
		tq := math.Sqrt(0.16 + math.Pow(0.015*dv[0], 2) + math.Pow(0.35*math.Pow(dv[1], 1.6), 2))
		t.Logf("D=%.0f V=%.2f  σpar=%.3f (true %.3f)  σperp=%.3f (true %.3f)", dv[0], dv[1], sp, tp, sq, tq)
		if dv[1] <= m.VMax*1.3 && (math.Abs(sp-tp)/tp > 0.2 || math.Abs(sq-tq)/tq > 0.2) {
			t.Errorf("noise not recovered at D=%v V=%v", dv[0], dv[1])
		}
	}
}

func TestAnalyzeReport(t *testing.T) {
	plays := simulatePlays(t, 24, 0)
	ep := analysis.EpochInfo{ID: 1, Setup: testSetup(), From: plays[0].Time.Add(-time.Hour), Established: true}
	start := time.Now()
	rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: []analysis.EpochInfo{ep}, Current: &ep, Now: plays[len(plays)-1].Time})
	t.Logf("analyze took %v", time.Since(start))
	c := rep.Coach
	t.Logf("phase=%s title=%q", c.Phase, c.Title)
	t.Logf("message=%s", c.Message)
	for _, e := range c.Evidence {
		t.Logf("  [%s] %s", e.Tone, e.Text)
	}
	if c.Next != nil {
		t.Logf("next=%+v change=%+v", *c.Next, *c.Change)
	}
	a := rep.Areas
	t.Logf("final=%+v\nfree=%+v\nshift=%+v\nrec=%+v", *a.Final, *a.Free, *a.Shifted, *a.Rec)
	cs := rep.Consistency
	t.Logf("best=%.2f [%.2f,%.2f] trust=[%.2f,%.2f] steadiness=%.3f exp=%.2f", cs.BestW, cs.BestLoW, cs.BestHiW, cs.TrustLoW, cs.TrustHiW, cs.Steadiness, cs.SpeedExp)
	for _, ct := range cs.Categories {
		t.Logf("  %s share=%.2f bestW=%.2f obs normErr=%.3f inside=%.3f", ct.Name, ct.Share, ct.BestW, ct.Current.NormErr, ct.Current.Inside)
	}
	for _, s := range cs.Insights {
		t.Logf("  * %s", s)
	}
	t.Logf("calibration=%v", cs.Calibration)
	t.Logf("aim: offset=(%.2f,%.2f) jump=%.2f%% pos=%.2f%% rot=%.2f field=%d dist=%d dirs=%d scatter=%d", rep.Aim.OffsetX, rep.Aim.OffsetY, rep.Aim.JumpGain, rep.Aim.PosGain, rep.Aim.Rotation, len(rep.Aim.Field), len(rep.Aim.Distance), len(rep.Aim.Directions), len(rep.Aim.Scatter))
	t.Logf("sessions=%d warmup=%v", len(rep.Progress.Sessions), rep.Progress.Warmup)
	if c.Phase != "set" || c.Next == nil {
		t.Errorf("expected a recommended area to set, got %s", c.Phase)
	}
	// the true centre of the hand is 1.2 mm right and 0.6 mm up
	if f := a.Final; math.Abs(f.X-(76+1.2)) > 0.4 || math.Abs(f.Y-(47.5-0.6)) > 0.4 {
		t.Errorf("recommended centre off: %.2f, %.2f", f.X, f.Y)
	}
}

// TestSizeOptimum checks that the compromise found from replays matches the
// optimum of the true player model.
func TestSizeOptimum(t *testing.T) {
	vs := TestMaps(1)
	var maps []*osu.Beatmap
	for _, v := range vs {
		bm, _ := osu.ParseBeatmap(v.Content)
		maps = append(maps, bm)
	}
	for _, name := range []string{"1124896", "801165"} {
		bm, err := osu.ParseBeatmapFile(filepath.Join(lazerBeatmaps, name+".osu"))
		if err != nil {
			t.Skip("maps missing")
		}
		maps = append(maps, bm)
	}
	for _, tc := range []struct {
		name    string
		area    otd.Rect
		a, c, p float64
	}{
		{"area piccola, mano tremolante", otd.Rect{W: 60, H: 33.75, X: 76, Y: 47.5}, 1.0, 1.5, 1.6},
		{"area grande, mano veloce-limitata", otd.Rect{W: 110, H: 61.9, X: 76, Y: 47.5}, 0.3, 30, 1.8},
	} {
		setup := testSetup()
		setup.Area = tc.area
		var plays []*analysis.PlayData
		for i := 0; i < 24; i++ {
			bm := maps[i%len(maps)]
			pl := &Player{APar: tc.a, BPar: 0.02, CPar: tc.c, P: tc.p, APerp: tc.a * 0.8, BPerp: 0.012, CPerp: tc.c * 0.6,
				TimingSD: 12, Rng: rand.New(rand.NewSource(int64(500 + i)))}
			frames := pl.Play(bm, 0, setup)
			objs, d := bm.Objects(0)
			hits, _ := analysis.Judge(objs, d, frames)
			plays = append(plays, &analysis.PlayData{ID: i + 1, Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour * 5),
				Epoch: 1, Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs()})
		}
		ep := analysis.EpochInfo{ID: 1, Setup: setup, From: plays[0].Time.Add(-time.Hour), Established: true}
		rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: []analysis.EpochInfo{ep}, Current: &ep, Now: plays[len(plays)-1].Time})
		cs := rep.Consistency
		// true optimum with the true model, same prediction points
		_, of := analysis.BuildSessions(plays)
		samples := analysis.BuildSamples(plays, of)
		ptrs := make([]*analysis.Sample, len(samples))
		for i := range samples {
			ptrs[i] = &samples[i]
		}
		trueM := analysis.NewNoiseModel(tc.a, 0.02, tc.c, tc.a*0.8, 0.012, tc.c*0.6, tc.p, cs.VMax)
		var w [analysis.NumCats]float64
		for i, ct := range cs.Categories {
			_ = i
			for k, key := range analysis.CatKeys {
				if key == ct.Key {
					w[k] = ct.Weight
				}
			}
		}
		truth := analysis.SweepSamples(analysis.NoiseFit{Model: trueM}, ptrs, setup.Mapper().MMPerPx(), 1, w)
		tw := truth.Best * tc.area.W
		t.Logf("%s: fitted a=%.2f exp=%.2f vmax=%.2f → best %.1f mm [%.1f, %.1f] trust [%.1f, %.1f]; true best %.1f mm", tc.name, cs.Steadiness, cs.SpeedExp, cs.VMax, cs.BestW, cs.BestLoW, cs.BestHiW, cs.TrustLoW, cs.TrustHiW, tw)
		t.Logf("   coach: %s — %s", rep.Coach.Phase, rep.Coach.Title)
		tol := 0.04 * tc.area.W
		if math.Abs(cs.BestW-tw) > tol && !(tw > cs.TrustHiW && cs.BestW >= cs.TrustHiW-0.6) && !(tw < cs.TrustLoW && cs.BestW <= cs.TrustLoW+0.6) {
			t.Errorf("%s: optimum %.1f mm far from truth %.1f mm", tc.name, cs.BestW, tw)
		}
	}
}

// TestFollowCompromiseLoop simulates a player who, after every map, sets the
// area suggested as compromise. The suggestions must settle instead of
// wandering, and the same-map comparisons must see the circle-size effect.
func TestFollowCompromiseLoop(t *testing.T) { followLoop(t, 0.6, 68, 40) }

// TestFollowCompromiseVariants: hands that adapt almost fully or hardly at
// all, starting from a too large area. The suggestions must settle.
func TestFollowCompromiseVariants(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	followLoop(t, 0.15, 100, 30)
	followLoop(t, 0.9, 100, 30)
}

func followLoop(t *testing.T, persist, startW float64, rounds int) {
	vs := TestMaps(1)
	var maps []*osu.Beatmap
	for _, v := range vs {
		bm, _ := osu.ParseBeatmap(v.Content)
		maps = append(maps, bm)
	}
	for _, name := range []string{"1124896", "801165"} {
		bm, err := osu.ParseBeatmapFile(filepath.Join(lazerBeatmaps, name+".osu"))
		if err != nil {
			t.Skip("maps missing")
		}
		maps = append(maps, bm)
	}
	const natural = 84.0 // absolute width the hand likes
	area := otd.Rect{W: startW, H: startW * 9 / 16, X: 76, Y: 47.5}
	base := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	var plays []*analysis.PlayData
	var epochs []analysis.EpochInfo
	var prev *otd.Rect
	var widths []float64
	for i := 0; i < rounds; i++ {
		setup := testSetup()
		setup.Area = area
		when := base.Add(time.Duration(i) * 3 * time.Hour)
		if len(epochs) == 0 || !epochs[len(epochs)-1].Setup.Area.Same(area) {
			if n := len(epochs); n > 0 {
				to := when.Add(-time.Minute)
				epochs[n-1].To = &to
			}
			epochs = append(epochs, analysis.EpochInfo{ID: len(epochs) + 1, Setup: setup, From: when.Add(-time.Minute)})
		}
		g := persist * (natural/area.W - 1) // partial adaptation to the area
		pl := &Player{Offset: [2]float64{0.8, -0.4}, Av: [2][2]float64{{g, 0}, {0, g}},
			APar: 0.9, BPar: 0.02, CPar: 6, P: 1.8, APerp: 0.75, BPerp: 0.012, CPerp: 3.6,
			TimingSD: 12, Rng: rand.New(rand.NewSource(int64(900 + i)))}
		bm := maps[i%len(maps)]
		frames := pl.Play(bm, 0, setup)
		objs, d := bm.Objects(0)
		hits, _ := analysis.Judge(objs, d, frames)
		plays = append(plays, &analysis.PlayData{ID: i + 1, Time: when.Add(2 * time.Minute), Epoch: epochs[len(epochs)-1].ID, Setup: setup,
			Rate: 1, Hits: hits, Duration: bm.LengthMs(), MapKey: bm.MD5 + "|0"})
		cur := epochs[len(epochs)-1]
		rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: epochs, Current: &cur, Now: when.Add(10 * time.Minute), PrevCompromise: prev})
		if rep.Areas.Final != nil {
			c := rep.Areas.Final.Rect
			prev = &c
			area = c
		}
		widths = append(widths, area.W)
		if d := rep.Consistency.Size; d != nil {
			r := rep.Areas.Rec
			t.Logf("map %2d: model %.1f final %.1f (%s) testing=%v kept=%v tried=[%.1f,%.1f] natural=%.1f persist=%.2f(%v)", i, d.ModelW, area.W, d.Source, r.Testing, r.Kept, r.ExploredLo, r.ExploredHi, rep.Consistency.Cross.NaturalW, rep.Consistency.Cross.Persist, rep.Consistency.Cross.PersistKnown)
		}
		if i == rounds-1 {
			cr := rep.Consistency.Cross
			t.Logf("natural width (all areas): %.1f ± %.1f mm [true %.0f], areas=%d mapsMulti=%d", cr.NaturalW, cr.NaturalWErr, natural, cr.Areas, cr.MapsMulti)
			for _, e := range cr.Effects {
				t.Logf("  %-16s maps=%d %s best %.1f vs ref %.1f (%+.1f%%)", e.Name, e.Maps, e.Verdict, e.BestW, e.RefW, 100*e.Gain)
			}
			if cr.AdaptCostOK {
				t.Logf("  adaptation cost: %+.1f%%", 100*cr.AdaptCost)
			}
			t.Logf("coach: %s — %s", rep.Coach.Phase, rep.Coach.Title)
		}
	}
	t.Logf("widths: %v", widths)
	last := widths[len(widths)-8:]
	lo, hi := last[0], last[0]
	for _, w := range last {
		lo, hi = math.Min(lo, w), math.Max(hi, w)
	}
	if hi-lo > 4 {
		t.Errorf("suggestions keep wandering: %.1f–%.1f mm", lo, hi)
	}
	if widths[len(widths)-1] < 70 {
		t.Errorf("did not move away from a too small area: %.1f", widths[len(widths)-1])
	}
}

// withCS returns a copy of a map with another circle size.
func withCS(t *testing.T, name string, cs float64) *osu.Beatmap {
	b, err := os.ReadFile(filepath.Join(lazerBeatmaps, name+".osu"))
	if err != nil {
		t.Skip("maps missing")
	}
	re := regexp.MustCompile(`(?m)^CircleSize:.*$`)
	b = re.ReplaceAll(b, []byte(fmt.Sprintf("CircleSize:%g\r", cs)))
	bm, err := osu.ParseBeatmap(b)
	if err != nil {
		t.Fatal(err)
	}
	return bm
}

// TestSameMapsAcrossAreas: the same maps (low and high circle size) are
// played with four different widths. The same-map comparisons must find the
// effect per circle size and the decision must stay inside the widths tried.
func TestSameMapsAcrossAreas(t *testing.T) {
	vs := TestMaps(1)
	var maps []*osu.Beatmap
	for _, v := range vs {
		bm, _ := osu.ParseBeatmap(v.Content)
		maps = append(maps, bm)
	}
	maps = append(maps, withCS(t, "1124896", 3.5), withCS(t, "801165", 3.5), withCS(t, "1124896", 4.5), withCS(t, "801165", 6.2), withCS(t, "1124896", 6.2))
	widths := []float64{72, 78, 84, 90}
	base := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	var plays []*analysis.PlayData
	var epochs []analysis.EpochInfo
	id := 0
	when := base
	for round := 0; round < 2; round++ {
		for wi, w := range widths {
			setup := testSetup()
			setup.Area = otd.Rect{W: w, H: w * 9 / 16, X: 76, Y: 47.5}
			eid := len(epochs) + 1
			if n := len(epochs); n > 0 {
				to := when.Add(-time.Minute)
				epochs[n-1].To = &to
			}
			epochs = append(epochs, analysis.EpochInfo{ID: eid, Setup: setup, From: when.Add(-time.Minute)})
			for k, bm := range maps {
				id++
				g := 0.6 * (84/w - 1)
				pl := &Player{Offset: [2]float64{0.6, -0.3}, Av: [2][2]float64{{g, 0}, {0, g}},
					APar: 0.7, BPar: 0.02, CPar: 3, P: 1.8, APerp: 0.6, BPerp: 0.012, CPerp: 1.8,
					TimingSD: 12, Rng: rand.New(rand.NewSource(int64(5000 + id)))}
				frames := pl.Play(bm, 0, setup)
				objs, d := bm.Objects(0)
				hits, _ := analysis.Judge(objs, d, frames)
				plays = append(plays, &analysis.PlayData{ID: id, Time: when, Epoch: eid, Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs(), MapKey: bm.MD5 + "|0"})
				when = when.Add(time.Duration(40+(k*7+wi*13)%50) * time.Minute)
			}
			when = when.Add(30 * time.Hour)
		}
	}
	cur := epochs[len(epochs)-1]
	rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: epochs, Current: &cur, Now: when})
	cr := rep.Consistency.Cross
	t.Logf("natural width: %.1f ± %.1f mm [true 84], persist %.2f ± %.2f known=%v [true 0.6], maps compared=%d (with current width %d)", cr.NaturalW, cr.NaturalWErr, cr.Persist, cr.PersistErr, cr.PersistKnown, cr.MapsMulti, cr.CurMaps)
	for _, e := range cr.Effects {
		t.Logf("  %-16s maps=%d %s best %.1f vs ref %.1f (%+.1f%%)", e.Name, e.Maps, e.Verdict, e.BestW, e.RefW, 100*e.Gain)
		for _, p := range e.Points {
			t.Logf("      %.1f mm: %+.1f%% [%+.1f, %+.1f] maps=%d plays=%d ref=%v", p.W, 100*p.Rel, 100*p.Lo, 100*p.Hi, p.Maps, p.Plays, p.Ref)
		}
	}
	t.Logf("  same-map optimum: known=%v %.1f [%.1f, %.1f]", cr.EmpBestKnown, cr.EmpBestW, cr.EmpBestLo, cr.EmpBestHi)
	if d := rep.Consistency.Size; d != nil {
		t.Logf("decision: model %.1f ± %.1f, final %.1f ± %.1f (%s)", d.ModelW, d.ModelErr, d.FinalW, d.FinalErr, d.Source)
		if d.FinalW < 72 || d.FinalW > 95 {
			t.Errorf("decision outside the widths tried: %.1f", d.FinalW)
		}
	}
	for _, b := range rep.Consistency.ByCS {
		t.Logf("  model %-14s share %.0f%% best %.1f mm", b.Name, 100*b.Share, b.BestS*cur.Setup.Area.W)
	}
	nCS := 0
	for _, e := range cr.Effects {
		if e.Group == "cs" {
			nCS++
		}
	}
	if nCS < 2 {
		t.Errorf("expected same-map effects for at least two circle-size groups, got %d", nCS)
	}
	t.Logf("coach: %s — %s", rep.Coach.Phase, rep.Coach.Title)
	for _, e := range rep.Coach.Evidence {
		t.Logf("   · %s", e.Text)
	}
}

// TestAdviceIndependentOfDriverArea: setting a new area in the driver, with
// no new replay, must not change the recommended area.
func TestAdviceIndependentOfDriverArea(t *testing.T) {
	vs := TestMaps(1)
	var maps []*osu.Beatmap
	for _, v := range vs {
		bm, _ := osu.ParseBeatmap(v.Content)
		maps = append(maps, bm)
	}
	maps = append(maps, withCS(t, "1124896", 4), withCS(t, "801165", 5))
	base := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	var plays []*analysis.PlayData
	var epochs []analysis.EpochInfo
	id := 0
	when := base
	for ei, w := range []float64{72, 80} {
		setup := testSetup()
		setup.Area = otd.Rect{W: w, H: w * 9 / 16, X: 76, Y: 47.5}
		epochs = append(epochs, analysis.EpochInfo{ID: ei + 1, Setup: setup, From: when.Add(-time.Minute)})
		for r := 0; r < 2; r++ {
			for _, bm := range maps {
				id++
				g := 0.5 * (84/w - 1)
				pl := &Player{Offset: [2]float64{0.8, -0.4}, Av: [2][2]float64{{g, 0}, {0, g}},
					APar: 0.7, BPar: 0.02, CPar: 3, P: 1.8, APerp: 0.6, BPerp: 0.012, CPerp: 1.8,
					TimingSD: 12, Rng: rand.New(rand.NewSource(int64(7000 + id)))}
				frames := pl.Play(bm, 0, setup)
				objs, d := bm.Objects(0)
				hits, _ := analysis.Judge(objs, d, frames)
				plays = append(plays, &analysis.PlayData{ID: id, Time: when, Epoch: ei + 1, Setup: setup, Rate: 1, Hits: hits, Duration: bm.LengthMs(), MapKey: bm.MD5 + "|0"})
				when = when.Add(50 * time.Minute)
			}
			when = when.Add(20 * time.Hour)
		}
	}
	to := when.Add(-time.Minute)
	epochs[1].To = &to
	cur := epochs[1]
	rep1 := analysis.Analyze(analysis.Input{Plays: plays, Epochs: epochs, Current: &cur, Now: when})
	f1 := rep1.Areas.Final.Rect
	t.Logf("with 80 mm set: final %+v (%s), free %+v", f1, rep1.Consistency.Size.Source, rep1.Areas.Free.Rect)
	// the player sets an area never used, without playing
	for _, w := range []float64{90.72, 66} {
		setup := testSetup()
		setup.Area = otd.Rect{W: w, H: w * 9 / 16, X: 70, Y: 50}
		eps := append(append([]analysis.EpochInfo{}, epochs...), analysis.EpochInfo{ID: 3, Setup: setup, From: when})
		cur2 := eps[2]
		for _, prev := range []*otd.Rect{&f1, nil} {
			rep2 := analysis.Analyze(analysis.Input{Plays: plays, Epochs: eps, Current: &cur2, Now: when.Add(time.Minute), PrevCompromise: prev})
			f2 := rep2.Areas.Final.Rect
			t.Logf("with %.2f mm set (prev %v): final %+v — coach: %s", w, prev != nil, f2, rep2.Coach.Title)
			if math.Abs(f2.W-f1.W) > 0.01 || math.Abs(f2.X-f1.X) > 0.01 || math.Abs(f2.Y-f1.Y) > 0.01 {
				t.Errorf("advice changed after setting %.2f mm without playing: %+v → %+v", w, f1, f2)
			}
		}
	}
}

// TestPositionDoesNotDrift: two players, both following the recommended
// area after every map. The first lands 1.2 mm right of every circle
// wherever the area is (the offset is in the way it aims): the advice must
// not chase it to the right. The second keeps the hand at x = 80 mm on the
// tablet: the advice must move there.
func TestPositionDoesNotDrift(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for _, c := range []struct {
		name  string
		q, h  float64
		d0    float64
		check func(xs []float64) string
	}{
		{"offset that follows the area", 0, 0, 1.2, func(xs []float64) string {
			if xs[len(xs)-1] > xs[0]+3 {
				return fmt.Sprintf("the area drifted right: %.1f → %.1f", xs[0], xs[len(xs)-1])
			}
			return ""
		}},
		{"mano ferma a 80 mm", 0.9, 80, 0, func(xs []float64) string {
			if math.Abs(xs[len(xs)-1]-80) > 1.2 {
				return fmt.Sprintf("did not reach the hand: %.1f", xs[len(xs)-1])
			}
			return ""
		}},
	} {
		vs := TestMaps(1)
		var maps []*osu.Beatmap
		for _, v := range vs {
			bm, _ := osu.ParseBeatmap(v.Content)
			maps = append(maps, bm)
		}
		area := otd.Rect{W: 80, H: 45, X: 74, Y: 47.5}
		base := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
		var plays []*analysis.PlayData
		var epochs []analysis.EpochInfo
		var prev *otd.Rect
		var xs []float64
		for i := 0; i < 36; i++ {
			setup := testSetup()
			setup.Area = area
			when := base.Add(time.Duration(i) * 3 * time.Hour)
			if len(epochs) == 0 || !epochs[len(epochs)-1].Setup.Area.Same(area) {
				if n := len(epochs); n > 0 {
					to := when.Add(-time.Minute)
					epochs[n-1].To = &to
				}
				epochs = append(epochs, analysis.EpochInfo{ID: len(epochs) + 1, Setup: setup, From: when.Add(-time.Minute)})
			}
			off := c.d0 + c.q*(c.h-area.X)
			pl := &Player{Offset: [2]float64{off, 0}, APar: 0.5, BPar: 0.02, CPar: 1, P: 1.6, APerp: 0.4, BPerp: 0.012, CPerp: 0.6,
				TimingSD: 12, Rng: rand.New(rand.NewSource(int64(3100 + i)))}
			bm := maps[i%len(maps)]
			frames := pl.Play(bm, 0, setup)
			objs, d := bm.Objects(0)
			hits, _ := analysis.Judge(objs, d, frames)
			plays = append(plays, &analysis.PlayData{ID: i + 1, Time: when.Add(2 * time.Minute), Epoch: epochs[len(epochs)-1].ID, Setup: setup,
				Rate: 1, Hits: hits, Duration: bm.LengthMs(), MapKey: bm.MD5 + "|0"})
			cur := epochs[len(epochs)-1]
			rep := analysis.Analyze(analysis.Input{Plays: plays, Epochs: epochs, Current: &cur, Now: when.Add(10 * time.Minute), PrevCompromise: prev})
			if rep.Areas.Final != nil {
				f := rep.Areas.Final.Rect
				prev = &f
				area.X, area.Y = f.X, f.Y // only the position is followed
				prev.W, prev.H = area.W, area.H
			}
			xs = append(xs, area.X)
			if i == 35 {
				cr := rep.Consistency.Cross
				t.Logf("%s: q_x=%.2f known=%v follows=%v", c.name, cr.PosPersistX, cr.PosKnownX, cr.PosFollowsX)
			}
		}
		t.Logf("%s: centre x %v", c.name, xs)
		if msg := c.check(xs); msg != "" {
			t.Errorf("%s: %s", c.name, msg)
		}
	}
}
