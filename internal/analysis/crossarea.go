package analysis

import (
	"math"
	"math/rand"
	"sort"
)

// Circle-size buckets (effective CS, after HR/EZ).
const NumCSB = 4

// CSBNames are the user-facing names of the circle-size buckets.
var CSBNames = [NumCSB]string{"CS up to 4", "CS 4 to 5", "CS 5 to 6", "CS 6 and above"}

// csBucket maps a radius in osu!pixels to its circle-size bucket.
func csBucket(rpx float64) int {
	cs := (54.4 - rpx) / 4.48
	switch {
	case cs < 3.95:
		return 0
	case cs < 4.95:
		return 1
	case cs < 5.95:
		return 2
	}
	return 3
}

// WidthPoint is the same-map error with a group of similar widths, relative
// to the reference width (the one in use, or the closest one tried).
type WidthPoint struct {
	Bin   int     `json:"bin"` // index into CrossArea.BinW
	W     float64 `json:"w"`
	Rel   float64 `json:"rel"` // 0.1 = 10% more error than the reference
	Lo    float64 `json:"lo"`
	Hi    float64 `json:"hi"`
	Maps  int     `json:"maps"`
	Plays int     `json:"plays"`
	Ref   bool    `json:"ref"`
}

// SameMapEffect is how the error on one kind of note changes with the area
// width, measured only by comparing plays of the same map made with
// different areas (so map difficulty cancels out).
type SameMapEffect struct {
	Key    string       `json:"key"`
	Name   string       `json:"name"`
	Group  string       `json:"group"` // "cs", "cat", "all" or "local"
	Maps   int          `json:"maps"`
	Plays  int          `json:"plays"`
	Notes  int          `json:"notes"`
	Points []WidthPoint `json:"points"`
	RefW   float64      `json:"refW"`
	BestW  float64      `json:"bestW"`
	Gain   float64      `json:"gain"` // error at BestW relative to RefW
	// curve verdict: bigger/smaller (a width that way is clearly better),
	// current (every other width tried is clearly worse), unclear
	Verdict  string  `json:"verdict"`
	WidthMin float64 `json:"widthMin"` // range of widths compared
	WidthMax float64 `json:"widthMax"`
}

// CrossArea summarises what all replays, made with different areas, say.
type CrossArea struct {
	Areas        int     `json:"areas"`
	Plays        int     `json:"plays"`
	NaturalW     float64 `json:"naturalW"` // absolute width matching the hand, all areas
	NaturalWErr  float64 `json:"naturalWErr"`
	NaturalX     float64 `json:"naturalX"`
	NaturalY     float64 `json:"naturalY"`
	NaturalXErr  float64 `json:"naturalXErr"`
	NaturalYErr  float64 `json:"naturalYErr"`
	NaturalKnown bool    `json:"naturalKnown"`
	// position: share of the offset that stays after moving the area, per
	// axis; "follows" means the offset moves with the area (it is in the way
	// you aim), so moving the area does not fix it
	PosPersistX float64 `json:"posPersistX"`
	PosPersistY float64 `json:"posPersistY"`
	PosKnownX   bool    `json:"posKnownX"`
	PosKnownY   bool    `json:"posKnownY"`
	PosFollowsX bool    `json:"posFollowsX"`
	PosFollowsY bool    `json:"posFollowsY"`
	HandRatio   float64 `json:"handRatio"` // natural height / width of the movement
	HandRot     float64 `json:"handRot"`   // rotation of the movement (degrees, absolute)
	HandRotErr  float64 `json:"handRotErr"`
	// share of the area/hand mismatch that stays after adapting
	Persist      float64         `json:"persist"`
	PersistErr   float64         `json:"persistErr"`
	PersistKnown bool            `json:"persistKnown"`
	Effects      []SameMapEffect `json:"effects"`
	AdaptCost    float64         `json:"adaptCost"` // extra error right after an area change
	AdaptCostOK  bool            `json:"adaptCostOk"`
	Combined     *SameMapEffect  `json:"combined"`
	MapsMulti    int             `json:"mapsMulti"` // maps played with at least two widths
	CurMaps      int             `json:"curMaps"`   // of those, played with the current width too
	BinW         []float64       `json:"binW"`      // mean width of each group of similar widths
	// width with the lowest same-map error, when the widths tried surround it
	EmpBestKnown bool    `json:"empBestKnown"`
	EmpBestW     float64 `json:"empBestW"`
	EmpBestLo    float64 `json:"empBestLo"`
	EmpBestHi    float64 `json:"empBestHi"`
}

type playAgg struct {
	id    int
	key   string
	width float64
	when  float64
	adapt float64 // 1 right after an area change, → 0 once used to it
	n     [NumCats + NumCSB + 1]int
	sq    [NumCats + NumCSB + 1]float64
}

// crossArea builds the pooled natural size and the same-map comparisons.
func crossArea(in Input, samples []*Sample, eds map[int]*epochData, curID int, curW float64, rng *rand.Rand) CrossArea {
	var ca CrossArea
	// --- natural size and centre from every area with a systematic fit
	latest := 0.0
	for _, s := range samples {
		latest = math.Max(latest, s.When)
	}
	type natObs struct {
		w, n, wt, wtP float64 // area width, measured natural width, weights
		h, iw, ih     float64 // area height, free-ratio natural width and height
		x, y, rot     float64 // where the hand lands, its rotation
		ax, ay        float64 // centre of the area
	}
	var nobs []natObs
	var sw, sx, sy float64
	for _, ed := range eds {
		if !ed.bias.OK() || ed.aim < 300 {
			continue
		}
		end := 0.0
		for _, s := range ed.fitS {
			end = math.Max(end, s.When)
		}
		// recent areas count more; the area set in the driver right now does
		// not count more than any other, so changing it changes nothing here
		w := float64(ed.aim) * math.Exp(-(latest-end)/86400/30)
		wp := math.Min(float64(ed.aim), 4000) // one long period must not hide the others
		if !ed.info.Established && !ed.info.Adapted && ed.aim < minAdaptHits {
			w *= 0.5  // not used to it yet: the systematic error is still moving
			wp *= 0.3 // and it says little about what stays after adapting
		}
		ar := ed.info.Setup.Area
		nobs = append(nobs, natObs{w: ar.W, n: ed.set.Lambda * ar.W, wt: w, wtP: wp, h: ar.H, iw: ed.set.Ideal.W, ih: ed.set.Ideal.H,
			x: ar.X + ed.bias.D[0], y: ar.Y + ed.bias.D[1], rot: ed.set.Ideal.Rot, ax: ar.X, ay: ar.Y})
		sw += w
		sx += w * (ar.X + ed.bias.D[0])
		sy += w * (ar.Y + ed.bias.D[1])
		ca.Areas++
	}
	// How much of the mismatch between area and hand survives adaptation:
	// with area w the hand aims at n = w + p·(N − w). Areas of different
	// widths measure p, and N is the width where the mismatch vanishes.
	xs, ys, ws := make([]float64, len(nobs)), make([]float64, len(nobs)), make([]float64, len(nobs))
	for i, o := range nobs {
		xs[i], ys[i], ws[i] = o.w, o.n-o.w, o.wtP
	}
	p, pSE, known := persistFit(xs, ys, ws, 4, persistFrac, 0.2, 0.6)
	ca.PersistKnown = known
	// The same for the position: with the area centred at X the hand lands
	// D = q·(H − X) away. If D stays the same wherever the area is (q ≈ 0),
	// the offset is in the way you aim, not in the position of the area, and
	// moving the area would only chase it.
	var pos [2]struct {
		q, qSE float64
		known  bool
	}
	for ax := 0; ax < 2; ax++ {
		for i, o := range nobs {
			if ax == 0 {
				xs[i], ys[i] = o.ax, o.x-o.ax
			} else {
				xs[i], ys[i] = o.ay, o.y-o.ay
			}
		}
		pos[ax].q, pos[ax].qSE, pos[ax].known = persistFit(xs, ys, ws, 1.5, 0.5, 0.3, 0.25)
	}
	ca.PosPersistX, ca.PosPersistY = round(pos[0].q, 3), round(pos[1].q, 3)
	ca.PosKnownX, ca.PosKnownY = pos[0].known, pos[1].known
	ca.PosFollowsX = pos[0].known && pos[0].q+1.645*pos[0].qSE < 0.35
	ca.PosFollowsY = pos[1].known && pos[1].q+1.645*pos[1].qSE < 0.35
	if sw > 0 {
		// where the hand wants the centre of the area, per axis
		centre := func(ax int) (float64, float64) {
			var c, v, w2 float64
			est := make([]float64, len(nobs))
			for i, o := range nobs {
				a, land := o.ax, o.x
				if ax == 1 {
					a, land = o.ay, o.y
				}
				switch {
				case (ax == 0 && ca.PosFollowsX) || (ax == 1 && ca.PosFollowsY):
					est[i] = a // the offset follows the area: stay where you play
				case pos[ax].known:
					// move by the offset only as much as moving has been seen
					// to remove it: never chase an offset that comes back
					est[i] = a + (land-a)*clamp(pos[ax].q/0.5, 0, 1)
				default:
					est[i] = land
				}
				c += o.wt * est[i]
			}
			c /= sw
			for i, o := range nobs {
				v += o.wt * (est[i] - c) * (est[i] - c)
				w2 += o.wt * o.wt
			}
			neff := sw * sw / math.Max(w2, 1e-9)
			return c, math.Max(1.96*math.Sqrt(v/sw/math.Max(neff, 1)), 0.3)
		}
		cx, ex := centre(0)
		cy, ey := centre(1)
		ca.NaturalX, ca.NaturalY = round(cx, 2), round(cy, 2)
		ca.NaturalXErr, ca.NaturalYErr = round(ex, 2), round(ey, 2)
	}
	ca.Persist, ca.PersistErr = round(p, 3), round(1.96*pSE, 3)
	if sw > 0 {
		ca.NaturalKnown = true
		// each area gives an estimate of N; without a measured p keep the
		// width the hand shows now (the coach then moves in small steps)
		var sn, v, w2, dp float64
		est := make([]float64, len(nobs))
		for i, o := range nobs {
			est[i] = o.n
			if ca.PersistKnown {
				est[i] = o.w + (o.n-o.w)/p
			}
			sn += o.wt * est[i]
			dp += o.wt * (o.n - o.w) / (p * p)
		}
		nat := sn / sw
		dp /= sw
		for i, o := range nobs {
			v += o.wt * (est[i] - nat) * (est[i] - nat)
			w2 += o.wt * o.wt
		}
		neff := sw * sw / math.Max(w2, 1e-9)
		se := math.Sqrt(v/sw) / math.Sqrt(math.Max(neff, 1))
		if len(nobs) == 1 {
			se = 0
		}
		if ca.PersistKnown {
			se = math.Hypot(se, dp*pSE) // uncertainty of p
		}
		// never claim more precision than about half a millimetre
		ca.NaturalW = round(nat, 2)
		ca.NaturalWErr = round(math.Max(se*1.96, 0.5), 2)
		// proportions and rotation of the hand's own movement (free ratio)
		var shw, shh, srot, vrot float64
		for _, o := range nobs {
			nw, nh := o.iw, o.ih
			if ca.PersistKnown {
				nw = o.w + (o.iw-o.w)/p
				nh = o.h + (o.ih-o.h)/p
			}
			shw += o.wt * nw
			shh += o.wt * nh
			srot += o.wt * o.rot
		}
		if shw > 0 && shh > 0 {
			ca.HandRatio = round(shh/shw, 4)
			ca.HandRot = round(srot/sw, 2)
			for _, o := range nobs {
				vrot += o.wt * (o.rot - srot/sw) * (o.rot - srot/sw)
			}
			ca.HandRotErr = round(math.Max(1.96*math.Sqrt(vrot/sw/math.Max(neff, 1)), 0.5), 2)
			if len(nobs) == 1 {
				ca.HandRotErr = 1
			}
		}
	}

	// --- per-play aggregates
	widthOf := map[int]float64{}
	for _, e := range in.Epochs {
		widthOf[e.ID] = e.Setup.Area.W
	}
	keyOf := map[int]string{}
	for _, p := range in.Plays {
		keyOf[p.ID] = p.MapKey
	}
	aggs := map[int]*playAgg{}
	for _, s := range samples {
		if s.Epoch <= 0 || !isAimHit(s) {
			continue
		}
		a := aggs[s.Play]
		if a == nil {
			a = &playAgg{id: s.Play, key: keyOf[s.Play], width: widthOf[s.Epoch], when: s.When}
			aggs[s.Play] = a
		}
		e := math.Min(math.Hypot(s.ENx, s.ENy), 3)
		sq := e * e
		for _, g := range []int{int(s.Cat), NumCats + csBucket(s.Rpx), NumCats + NumCSB} {
			a.n[g]++
			a.sq[g] += sq
		}
	}
	// experience on the area at the time of each play
	byEpoch := map[int][]*playAgg{}
	epochOfPlay := map[int]int{}
	for _, s := range samples {
		if s.Epoch > 0 {
			epochOfPlay[s.Play] = s.Epoch
		}
	}
	for id, a := range aggs {
		byEpoch[epochOfPlay[id]] = append(byEpoch[epochOfPlay[id]], a)
	}
	infos := map[int]EpochInfo{}
	for _, e := range in.Epochs {
		infos[e.ID] = e
	}
	for eid, list := range byEpoch {
		sort.Slice(list, func(i, j int) bool { return list[i].when < list[j].when })
		before := 0
		for _, a := range list {
			if inf, ok := infos[eid]; ok && (inf.Established || inf.Adapted) {
				a.adapt = 0
			} else {
				a.adapt = math.Exp(-float64(before) / 1500)
			}
			before += a.n[NumCats+NumCSB]
		}
	}
	ca.Plays = len(aggs)

	// group plays by map, keep maps played with at least two widths
	byMap := map[string][]*playAgg{}
	for _, a := range aggs {
		if a.key != "" && a.width > 0 {
			byMap[a.key] = append(byMap[a.key], a)
		}
	}
	var multi [][]*playAgg
	for _, list := range byMap {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, a := range list {
			lo, hi = math.Min(lo, a.width), math.Max(hi, a.width)
		}
		if hi/lo-1 >= 0.015 {
			multi = append(multi, list)
		}
	}
	ca.MapsMulti = len(multi)
	if len(multi) == 0 {
		return ca
	}
	for _, list := range multi {
		for _, a := range list {
			if math.Abs(a.width-curW) < 0.3 && a.n[NumCats+NumCSB] >= 25 {
				ca.CurMaps++
				break
			}
		}
	}
	t0 := math.Inf(1)
	for _, a := range aggs {
		t0 = math.Min(t0, a.when)
	}

	// within-map regression:
	//   log(error) = map + b1·x [+ b2·x²] + c·adaptation + d·time,  x = log(width/curW)
	// map difficulty cancels out because every map is compared only with
	// itself; adaptation and time absorb "just changed area" and "got better".
	type obs struct {
		x, q, a, t float64
		y, w       float64
	}
	type fitOpt struct {
		g      int
		quad   bool
		lo, hi float64 // only plays with widths inside (0 = no limit)
	}
	type fitRes struct {
		b1, b2, adapt float64
		ok            bool
		nm, np, nn    int
		wlo, whi      float64
		widths        int
	}
	ref := curW
	if ref <= 0 {
		ref = 80
	}
	fit := func(o fitOpt, maps []int) (r fitRes) {
		var data []obs
		r.wlo, r.whi = math.Inf(1), math.Inf(-1)
		distinct := map[int]bool{}
		for _, mi := range maps {
			var sel []*playAgg
			for _, a := range multi[mi] {
				if a.n[o.g] < 25 {
					continue
				}
				if o.hi > 0 && (a.width < o.lo || a.width > o.hi) {
					continue
				}
				sel = append(sel, a)
			}
			if len(sel) < 2 {
				continue
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, a := range sel {
				lo, hi = math.Min(lo, a.width), math.Max(hi, a.width)
			}
			if hi/lo-1 < 0.015 {
				continue
			}
			r.nm++
			var W, mx, mq, ma, mt, my float64
			for _, a := range sel {
				w := math.Sqrt(float64(a.n[o.g]))
				x := math.Log(a.width / ref)
				err := math.Sqrt(a.sq[o.g] / float64(a.n[o.g]) / 2)
				W += w
				mx += w * x
				mq += w * x * x
				ma += w * a.adapt
				mt += w * (a.when - t0) / 86400 / 30
				my += w * math.Log(math.Max(err, 1e-3))
			}
			mx, mq, ma, mt, my = mx/W, mq/W, ma/W, mt/W, my/W
			for _, a := range sel {
				w := math.Sqrt(float64(a.n[o.g]))
				x := math.Log(a.width / ref)
				err := math.Sqrt(a.sq[o.g] / float64(a.n[o.g]) / 2)
				data = append(data, obs{x: x - mx, q: x*x - mq, a: a.adapt - ma, t: (a.when-t0)/86400/30 - mt,
					y: math.Log(math.Max(err, 1e-3)) - my, w: w})
				r.np++
				r.nn += a.n[o.g]
				r.wlo, r.whi = math.Min(r.wlo, a.width), math.Max(r.whi, a.width)
				distinct[int(math.Round(a.width*2))] = true
			}
		}
		r.widths = len(distinct)
		if r.nm < 2 || r.np < 5 {
			return
		}
		k := 3
		if o.quad {
			k = 4
			if r.widths < 3 {
				return
			}
		}
		A := make([][]float64, k)
		B := make([][]float64, k)
		for i := range A {
			A[i] = make([]float64, k)
			B[i] = []float64{0}
		}
		for _, d := range data {
			v := []float64{d.x, d.a, d.t, d.q}[:k]
			for a := 0; a < k; a++ {
				for b := 0; b < k; b++ {
					A[a][b] += d.w * v[a] * v[b]
				}
				B[a][0] += d.w * v[a] * d.y
			}
		}
		if A[0][0] < 1e-6 {
			return
		}
		// a small ridge on adaptation and time
		A[1][1] += 0.5
		A[2][2] += 0.5
		x, good := solve(A, B)
		if !good {
			return
		}
		r.b1, r.adapt, r.ok = x[0][0], x[1][0], true
		if o.quad {
			r.b2 = x[3][0]
		}
		return
	}
	allMaps := func() []int {
		idx := make([]int, len(multi))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	boot := func(o fitOpt, reps int, f func(fitRes)) {
		idx := make([]int, len(multi))
		for r := 0; r < reps; r++ {
			for i := range idx {
				idx[i] = rng.Intn(len(multi))
			}
			if fr := fit(o, idx); fr.ok {
				f(fr)
			}
		}
	}
	bins := widthBins(multi)
	{
		sum := make([]float64, len(bins))
		cnt := make([]float64, len(bins))
		for _, list := range multi {
			for _, a := range list {
				b := binOf(bins, a.width)
				sum[b] += a.width
				cnt[b]++
			}
		}
		ca.BinW = make([]float64, len(bins))
		for b := range bins {
			if cnt[b] > 0 {
				ca.BinW[b] = round(sum[b]/cnt[b], 1)
			}
		}
	}
	for b := 0; b < NumCSB; b++ {
		if e := sameMapCurve(multi, bins, NumCats+b, t0, curW, rng); e != nil {
			e.Key, e.Name, e.Group = "cs"+string(rune('0'+b)), CSBNames[b], "cs"
			ca.Effects = append(ca.Effects, *e)
		}
	}
	for k := 0; k < NumCats; k++ {
		if k == CatOther {
			continue
		}
		if e := sameMapCurve(multi, bins, k, t0, curW, rng); e != nil {
			e.Key, e.Name, e.Group = CatKeys[k], CatNames[k], "cat"
			ca.Effects = append(ca.Effects, *e)
		}
	}
	gAll := NumCats + NumCSB
	if e := sameMapCurve(multi, bins, gAll, t0, curW, rng); e != nil {
		e.Key, e.Name, e.Group = "all", "All notes", "all"
		ca.Combined = e
	}
	// the extra error right after a change (all notes)
	if fr := fit(fitOpt{g: gAll}, allMaps()); fr.ok {
		ca.AdaptCost = round(math.Exp(fr.adapt)-1, 4)
		ca.AdaptCostOK = true
	}
	// the width where the same-map error is lowest, when the widths tried
	// surround it (a U-shaped curve)
	qo := fitOpt{g: gAll, quad: true}
	if fr := fit(qo, allMaps()); fr.ok && fr.nm >= 4 && fr.widths >= 3 && fr.whi/fr.wlo >= 1.08 && fr.b2 > 0 {
		opt := func(b fitRes) float64 {
			if b.b2 <= 0 {
				if b.b1 < 0 {
					return math.Inf(1)
				}
				return math.Inf(-1)
			}
			return ref * math.Exp(-b.b1/(2*b.b2))
		}
		w0 := opt(fr)
		var reps []float64
		inside := 0
		boot(qo, 150, func(b fitRes) {
			w := opt(b)
			if w >= fr.wlo && w <= fr.whi {
				inside++
			}
			reps = append(reps, clamp(w, fr.wlo, fr.whi))
		})
		// the smooth curve must agree with the widths compared one by one
		agrees := ca.Combined == nil || math.Abs(ca.Combined.BestW-w0) <= 0.05*w0
		if agrees && w0 >= fr.wlo && w0 <= fr.whi && len(reps) >= 50 && float64(inside) >= 0.8*float64(len(reps)) {
			ca.EmpBestKnown = true
			ca.EmpBestW = round(w0, 2)
			ca.EmpBestLo = round(quantile(reps, 0.1), 2)
			ca.EmpBestHi = round(quantile(reps, 0.9), 2)
		}
	}
	return ca
}

// SizeDecision says how the recommended width was chosen from the precision
// model and the same-map comparisons. Everything is in absolute millimetres:
// the area set in the driver right now plays no part.
type SizeDecision struct {
	ModelW   float64 `json:"modelW"`
	ModelErr float64 `json:"modelErr"`
	EmpKnown bool    `json:"empKnown"`
	EmpW     float64 `json:"empW"`
	EmpLo    float64 `json:"empLo"`
	EmpHi    float64 `json:"empHi"`
	FinalW   float64 `json:"finalW"`
	FinalErr float64 `json:"finalErr"`
	// model: precision model only; combined: model and same-map optimum
	// merged; evidence: the model's width was clearly worse on the same maps
	// than another width tried, which wins; confirmed: the same maps agree.
	Source   string  `json:"source"`
	EvidW    float64 `json:"evidW"`   // the width tried that settled it
	EvidRel  float64 `json:"evidRel"` // error of the model's width vs it
	EvidMaps int     `json:"evidMaps"`
}

// decideSize merges the model optimum (modelErr: 90% half-width, mm) with
// what the same maps played with different widths say. lo and hi bound the
// widths the advice may use.
func decideSize(modelW, modelErr float64, ca CrossArea, lo, hi float64) SizeDecision {
	d := SizeDecision{ModelW: round(modelW, 2), ModelErr: round(modelErr, 2), FinalW: modelW, FinalErr: modelErr, Source: "model"}
	if ca.EmpBestKnown {
		d.EmpKnown, d.EmpW, d.EmpLo, d.EmpHi = true, ca.EmpBestW, ca.EmpBestLo, ca.EmpBestHi
		sm := math.Max(modelErr/1.645, 0.3)
		se := math.Max((ca.EmpBestHi-ca.EmpBestLo)/2.56, 0.5)
		wm, we := 1/(sm*sm), 1/(se*se)
		d.FinalW = (modelW*wm + ca.EmpBestW*we) / (wm + we)
		d.FinalErr = 1.645 / math.Sqrt(wm+we)
		d.Source = "combined"
	} else if cb := ca.Combined; cb != nil && len(cb.Points) >= 2 {
		pts := append([]WidthPoint(nil), cb.Points...)
		sort.Slice(pts, func(i, j int) bool { return pts[i].W < pts[j].W })
		best := &pts[0]
		for i := range pts {
			if pts[i].Rel < best.Rel {
				best = &pts[i]
			}
		}
		// the same-map error expected at width w, from the widths tried
		// around it (only when they are close enough to interpolate)
		at := func(w float64) (rel, l, h float64, maps int, ok bool) {
			for i := range pts {
				if math.Abs(pts[i].W-w) <= 0.03*w {
					p := pts[i]
					return p.Rel, p.Lo, p.Hi, p.Maps, true
				}
			}
			for i := 1; i < len(pts); i++ {
				a, b := pts[i-1], pts[i]
				if w > a.W && w < b.W && b.W/a.W-1 <= 0.08 {
					t := (w - a.W) / (b.W - a.W)
					lerp := func(x, y float64) float64 { return x + t*(y-x) }
					return lerp(a.Rel, b.Rel), lerp(a.Lo, b.Lo), lerp(a.Hi, b.Hi), int(math.Min(float64(a.Maps), float64(b.Maps))), true
				}
			}
			return 0, 0, 0, 0, false
		}
		// clearly worse than the best width tried (intervals are relative to
		// the reference width)
		bestHi := best.Hi
		if best.Ref {
			bestHi = 0
		}
		worse := func(l float64) bool { return l > bestHi }
		if rel, l, _, _, ok := at(modelW); ok && math.Abs(modelW-best.W) > 0.5 && worse(l) {
			d.Source = "evidence"
			d.EvidW, d.EvidMaps = best.W, best.Maps
			d.EvidRel = (1+rel)/(1+best.Rel) - 1
			d.FinalW = best.W
			d.FinalErr = math.Max(modelErr, 1)
			// the model points far away: explore one step from the best width
			// towards it, unless that step is already known to be worse
			if dist := (modelW - best.W) / best.W; math.Abs(dist) > 2*0.06 {
				step := best.W * (1 + math.Copysign(0.06, dist))
				if _, l2, _, _, ok2 := at(step); !ok2 || !worse(l2) {
					d.FinalW = clamp(step, lo, hi)
					d.Source = "evidence-step"
				}
			}
		} else if ok && math.Abs(modelW-best.W) <= 0.03*modelW {
			clear := true
			for i := range pts {
				if p := &pts[i]; p.W != best.W && !worse(p.Lo) {
					clear = false
				}
			}
			if clear {
				d.Source = "confirmed"
				d.EvidW, d.EvidMaps = best.W, best.Maps
			}
		}
	}
	d.FinalW = round(d.FinalW, 2)
	d.FinalErr = round(d.FinalErr, 2)
	return d
}

// widthBins groups the widths tried into bins about 2.5% wide, so that tiny
// changes (82.3 → 83.1 mm) count as the same size.
func widthBins(multi [][]*playAgg) []float64 {
	var ws []float64
	for _, list := range multi {
		for _, a := range list {
			ws = append(ws, a.width)
		}
	}
	sort.Float64s(ws)
	var edges []float64 // lower edge of each bin
	for _, w := range ws {
		if len(edges) == 0 || w > edges[len(edges)-1]*1.025 {
			edges = append(edges, w)
		}
	}
	return edges
}

func binOf(edges []float64, w float64) int {
	b := 0
	for i, e := range edges {
		if w >= e-1e-9 {
			b = i
		}
	}
	return b
}

// sameMapCurve estimates, for one group of notes, the error with every
// width bin relative to the reference bin, comparing each map only with
// itself and accounting for adaptation and improvement over time.
func sameMapCurve(multi [][]*playAgg, edges []float64, g int, t0, curW float64, rng *rand.Rand) *SameMapEffect {
	nb := len(edges)
	if nb < 2 {
		return nil
	}
	type play struct {
		a   *playAgg
		bin int
	}
	// maps usable for this group: plays with enough notes in ≥2 bins
	var maps [][]play
	for _, list := range multi {
		var ps []play
		seen := map[int]bool{}
		for _, a := range list {
			if a.n[g] >= 25 {
				b := binOf(edges, a.width)
				ps = append(ps, play{a, b})
				seen[b] = true
			}
		}
		if len(seen) >= 2 {
			maps = append(maps, ps)
		}
	}
	// keep bins seen on at least two maps (one, if there is only one map)
	minMaps := 2
	if len(maps) < 2 {
		minMaps = 1
	}
	var keep []bool
	for iter := 0; iter < 3; iter++ {
		cnt := make([]int, nb)
		for _, ps := range maps {
			seen := map[int]bool{}
			for _, p := range ps {
				seen[p.bin] = true
			}
			for b := range seen {
				cnt[b]++
			}
		}
		keep = make([]bool, nb)
		for b := range cnt {
			keep[b] = cnt[b] >= minMaps
		}
		var next [][]play
		for _, ps := range maps {
			var q []play
			seen := map[int]bool{}
			for _, p := range ps {
				if keep[p.bin] {
					q = append(q, p)
					seen[p.bin] = true
				}
			}
			if len(seen) >= 2 {
				next = append(next, q)
			}
		}
		maps = next
	}
	if len(maps) == 0 {
		return nil
	}
	var kept []int
	for b := 0; b < nb; b++ {
		if keep[b] {
			kept = append(kept, b)
		}
	}
	if len(kept) < 2 {
		return nil
	}
	// mean width of each kept bin, and the reference bin (closest to now)
	bw := make([]float64, nb)
	bn := make([]float64, nb)
	bmaps := make([]int, nb)
	nn := 0
	for _, ps := range maps {
		seen := map[int]bool{}
		for _, p := range ps {
			bw[p.bin] += p.a.width
			bn[p.bin]++
			nn += p.a.n[g]
			seen[p.bin] = true
		}
		for b := range seen {
			bmaps[b]++
		}
	}
	for b := range bw {
		if bn[b] > 0 {
			bw[b] /= bn[b]
		}
	}
	ref := kept[0]
	for _, b := range kept {
		if math.Abs(bw[b]-curW) < math.Abs(bw[ref]-curW) {
			ref = b
		}
	}
	col := map[int]int{} // bin → column of its indicator
	for _, b := range kept {
		if b != ref {
			col[b] = len(col)
		}
	}
	k := len(col) + 2
	fit := func(sel []int) (beta []float64, cov [][]float64, ok bool) {
		var X [][]float64
		var Y, W []float64
		for _, mi := range sel {
			start := len(X)
			sw, my := 0.0, 0.0
			mx := make([]float64, k)
			for _, p := range maps[mi] {
				w := math.Sqrt(float64(p.a.n[g]))
				x := make([]float64, k)
				if c, ok := col[p.bin]; ok {
					x[c] = 1
				}
				x[k-2] = p.a.adapt
				x[k-1] = (p.a.when - t0) / 86400 / 30
				y := math.Log(math.Max(math.Sqrt(p.a.sq[g]/float64(p.a.n[g])/2), 1e-3))
				X = append(X, x)
				Y = append(Y, y)
				W = append(W, w)
				sw += w
				for j := range x {
					mx[j] += w * x[j]
				}
				my += w * y
			}
			for i := start; i < len(X); i++ {
				for j := range X[i] {
					X[i][j] -= mx[j] / sw
				}
				Y[i] -= my / sw
			}
		}
		A := make([][]float64, k)
		B := make([][]float64, k)
		for i := range A {
			A[i] = make([]float64, k)
			B[i] = []float64{0}
		}
		for r := range X {
			for i := 0; i < k; i++ {
				for j := 0; j < k; j++ {
					A[i][j] += W[r] * X[r][i] * X[r][j]
				}
				B[i][0] += W[r] * X[r][i] * Y[r]
			}
		}
		// a small ridge on adaptation and time
		A[k-2][k-2] += 0.5
		A[k-1][k-1] += 0.5
		x, good := solve(A, B)
		if !good {
			return nil, nil, false
		}
		beta = make([]float64, k)
		for i := range beta {
			beta[i] = x[i][0]
		}
		// residual variance (at least 4% of noise per play) → covariance
		rss, sw := 0.0, 0.0
		for r := range X {
			pred := 0.0
			for i := 0; i < k; i++ {
				pred += X[r][i] * beta[i]
			}
			rss += W[r] * (Y[r] - pred) * (Y[r] - pred)
			sw += W[r]
		}
		dof := float64(len(X) - len(sel) - k)
		s2 := 0.0
		if dof > 0 {
			s2 = rss / dof
		}
		s2 = math.Max(s2, 0.04*0.04*sw/float64(len(X)))
		I := make([][]float64, k)
		for i := range I {
			I[i] = make([]float64, k)
			I[i][i] = 1
		}
		inv, good := solve(A, I)
		if !good {
			return nil, nil, false
		}
		cov = make([][]float64, k)
		for i := range cov {
			cov[i] = make([]float64, k)
			for j := range cov[i] {
				cov[i][j] = s2 * inv[i][j]
			}
			if dof <= 0 {
				// no replication: the comparison has no measurable precision
				cov[i][i] = math.Max(cov[i][i], 0.15*0.15)
			}
		}
		return beta, cov, true
	}
	all := make([]int, len(maps))
	for i := range all {
		all[i] = i
	}
	beta, cov, ok := fit(all)
	if !ok {
		return nil
	}
	// cluster bootstrap over maps when there are enough of them
	var reps [][]float64
	if len(maps) >= 5 {
		idx := make([]int, len(maps))
		for r := 0; r < 120; r++ {
			for i := range idx {
				idx[i] = rng.Intn(len(maps))
			}
			if b, _, ok2 := fit(idx); ok2 {
				reps = append(reps, b)
			}
		}
	}
	e := &SameMapEffect{Maps: len(maps), Notes: nn, RefW: round(bw[ref], 1)}
	best, bestG, bestHi := ref, 0.0, 0.0
	allWorse := true
	for _, b := range kept {
		pt := WidthPoint{Bin: b, W: round(bw[b], 1), Maps: bmaps[b], Plays: int(bn[b])}
		e.Plays += int(bn[b])
		if b == ref {
			pt.Ref = true
			e.Points = append(e.Points, pt)
			continue
		}
		c := col[b]
		gm := beta[c]
		se := math.Sqrt(math.Max(cov[c][c], 0))
		lo, hi := gm-1.96*se, gm+1.96*se
		if len(reps) >= 30 {
			var xs []float64
			for _, r := range reps {
				xs = append(xs, r[c])
			}
			lo = math.Min(lo, quantile(xs, 0.025))
			hi = math.Max(hi, quantile(xs, 0.975))
		}
		pt.Rel, pt.Lo, pt.Hi = round(math.Exp(gm)-1, 4), round(math.Exp(lo)-1, 4), round(math.Exp(hi)-1, 4)
		e.Points = append(e.Points, pt)
		if gm < bestG {
			best, bestG, bestHi = b, gm, hi
		}
		if lo <= 0 {
			allWorse = false
		}
	}
	e.BestW = round(bw[best], 1)
	e.Gain = round(math.Exp(bestG)-1, 4)
	e.WidthMin, e.WidthMax = e.Points[0].W, e.Points[len(e.Points)-1].W
	switch {
	case best != ref && bestHi < 0 && bw[best] > bw[ref]:
		e.Verdict = "bigger"
	case best != ref && bestHi < 0:
		e.Verdict = "smaller"
	case best == ref && allWorse:
		e.Verdict = "current"
	default:
		e.Verdict = "unclear"
	}
	return e
}

// persistFit estimates q in y = q·(N − x), i.e. the slope −q of the offset y
// against the setting x, from areas with weights w. It needs a spread of at
// least minRange in x; the estimate is shrunk towards prior (sd priorSD),
// and the residual scatter per area is at least floorSD.
func persistFit(xs, ys, ws []float64, minRange, prior, priorSD, floorSD float64) (q, qSE float64, known bool) {
	q, qSE = prior, priorSD
	if len(xs) < 2 {
		return
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	var W, mx, my, w2 float64
	for i := range xs {
		lo, hi = math.Min(lo, xs[i]), math.Max(hi, xs[i])
		W += ws[i]
		mx += ws[i] * xs[i]
		my += ws[i] * ys[i]
		w2 += ws[i] * ws[i]
	}
	if hi-lo < minRange || W <= 0 {
		return
	}
	mx, my = mx/W, my/W
	var sxx, sxy float64
	for i := range xs {
		sxx += ws[i] * (xs[i] - mx) * (xs[i] - mx)
		sxy += ws[i] * (xs[i] - mx) * (ys[i] - my)
	}
	if sxx <= 0 {
		return
	}
	neff := W * W / math.Max(w2, 1e-9)
	b := sxy / sxx
	rss := 0.0
	for i := range xs {
		r := (ys[i] - my) - b*(xs[i]-mx)
		rss += ws[i] * r * r
	}
	s2 := math.Max(rss/W*neff/math.Max(neff-2, 1), floorSD*floorSD)
	se := math.Sqrt(s2 / (sxx / W * math.Max(neff, 2)))
	qm := clamp(-b, 0, 1)
	q = (qm/(se*se) + prior/(priorSD*priorSD)) / (1/(se*se) + 1/(priorSD*priorSD))
	qSE = 1 / math.Sqrt(1/(se*se)+1/(priorSD*priorSD))
	return clamp(q, 0.02, 1), qSE, true
}
