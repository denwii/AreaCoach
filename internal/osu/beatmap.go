package osu

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Object kinds.
const (
	KindCircle = iota
	KindSlider
	KindSpinner
)

// Vec is a 2D point in osu!pixels.
type Vec struct{ X, Y float64 }

func (a Vec) Add(b Vec) Vec             { return Vec{a.X + b.X, a.Y + b.Y} }
func (a Vec) Sub(b Vec) Vec             { return Vec{a.X - b.X, a.Y - b.Y} }
func (a Vec) Mul(s float64) Vec         { return Vec{a.X * s, a.Y * s} }
func (a Vec) Len() float64              { return math.Hypot(a.X, a.Y) }
func (a Vec) Dist(b Vec) float64        { return math.Hypot(a.X-b.X, a.Y-b.Y) }
func (a Vec) Dot(b Vec) float64         { return a.X*b.X + a.Y*b.Y }
func (a Vec) Equal(b Vec) bool          { return a.X == b.X && a.Y == b.Y }
func (a Vec) Lerp(b Vec, t float64) Vec { return Vec{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t} }

// TimingPoint as read from [TimingPoints].
type TimingPoint struct {
	Time        float64
	BeatLength  float64
	Uninherited bool
}

// HitObject is one object of the map after mods, stacking and slider math.
type HitObject struct {
	Index       int
	Kind        int
	Time        float64 // ms, map time
	EndTime     float64
	Pos         Vec // unstacked position (after HR flip)
	EndPos      Vec // unstacked slider end position (after repeats)
	StackHeight int
	Slides      int
	path        *sliderPath
	NewCombo    bool
}

// StackedPos is where the object is drawn and must be clicked.
func (h *HitObject) StackedPos(stackOffset float64) Vec {
	o := float64(h.StackHeight) * stackOffset
	return Vec{h.Pos.X - o, h.Pos.Y - o}
}

// StackedEndPos is the stacked slider end (where the cursor leaves the slider).
func (h *HitObject) StackedEndPos(stackOffset float64) Vec {
	o := float64(h.StackHeight) * stackOffset
	return Vec{h.EndPos.X - o, h.EndPos.Y - o}
}

// PathPositionAt returns the slider ball position at time t (unstacked).
func (h *HitObject) PathPositionAt(t float64) Vec {
	if h.Kind != KindSlider || h.path == nil || h.EndTime <= h.Time {
		return h.Pos
	}
	if t <= h.Time {
		return h.Pos
	}
	if t >= h.EndTime {
		return h.EndPos
	}
	span := (h.EndTime - h.Time) / float64(h.Slides)
	p := (t - h.Time) / span
	k := int(p)
	f := p - float64(k)
	if k%2 == 1 {
		f = 1 - f
	}
	return h.Pos.Add(h.path.positionAt(f * h.path.distance))
}

// Beatmap is a parsed .osu file.
type Beatmap struct {
	FormatVersion  int
	Mode           int
	Title          string
	Artist         string
	Creator        string
	Version        string
	Tags           string
	BeatmapID      int
	AudioFilename  string
	StackLeniency  float64
	HP, CS, OD, AR float64
	SliderMult     float64
	SliderTickRate float64
	TimingPoints   []TimingPoint
	raw            []rawObject
	MD5            string
}

type rawObject struct {
	x, y     float64
	time     float64
	typ      int
	endTime  float64
	curve    byte
	points   []Vec
	slides   int
	length   float64
	newCombo bool
}

// ParseBeatmapFile reads a .osu file from disk.
func ParseBeatmapFile(path string) (*Beatmap, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBeatmap(b)
}

// ParseBeatmap parses .osu content.
func ParseBeatmap(b []byte) (*Beatmap, error) {
	sum := md5.Sum(b)
	bm := &Beatmap{StackLeniency: 0.7, SliderMult: 1.4, SliderTickRate: 1, AR: -1, CS: 5, OD: 5, HP: 5, MD5: hex.EncodeToString(sum[:])}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	section := ""
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			first = false
			if strings.HasPrefix(line, "osu file format v") {
				bm.FormatVersion, _ = strconv.Atoi(strings.TrimPrefix(line, "osu file format v"))
				continue
			}
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}
		switch section {
		case "General", "Metadata", "Difficulty":
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			f, _ := strconv.ParseFloat(v, 64)
			switch k {
			case "Mode":
				bm.Mode = int(f)
			case "StackLeniency":
				bm.StackLeniency = f
			case "AudioFilename":
				bm.AudioFilename = v
			case "Title":
				bm.Title = v
			case "Artist":
				bm.Artist = v
			case "Creator":
				bm.Creator = v
			case "Version":
				bm.Version = v
			case "Tags":
				bm.Tags = v
			case "BeatmapID":
				bm.BeatmapID = int(f)
			case "HPDrainRate":
				bm.HP = f
			case "CircleSize":
				bm.CS = f
			case "OverallDifficulty":
				bm.OD = f
			case "ApproachRate":
				bm.AR = f
			case "SliderMultiplier":
				bm.SliderMult = f
			case "SliderTickRate":
				bm.SliderTickRate = f
			}
		case "TimingPoints":
			parts := strings.Split(line, ",")
			if len(parts) < 2 {
				continue
			}
			t, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			bl, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 != nil || err2 != nil {
				continue
			}
			un := true
			if len(parts) >= 7 {
				un = strings.TrimSpace(parts[6]) != "0"
			}
			if bl < 0 && len(parts) < 7 {
				un = false
			}
			bm.TimingPoints = append(bm.TimingPoints, TimingPoint{Time: t, BeatLength: bl, Uninherited: un})
		case "HitObjects":
			if o, ok := parseHitObject(line); ok {
				bm.raw = append(bm.raw, o)
			}
		}
	}
	if bm.AR < 0 {
		bm.AR = bm.OD
	}
	if bm.SliderMult <= 0 {
		bm.SliderMult = 1.4
	}
	sort.SliceStable(bm.TimingPoints, func(i, j int) bool { return bm.TimingPoints[i].Time < bm.TimingPoints[j].Time })
	sort.SliceStable(bm.raw, func(i, j int) bool { return bm.raw[i].time < bm.raw[j].time })
	if len(bm.raw) == 0 && bm.Mode == 0 {
		return bm, errors.New("the map has no hit objects")
	}
	return bm, nil
}

func parseHitObject(line string) (rawObject, bool) {
	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return rawObject{}, false
	}
	x, e1 := strconv.ParseFloat(parts[0], 64)
	y, e2 := strconv.ParseFloat(parts[1], 64)
	t, e3 := strconv.ParseFloat(parts[2], 64)
	typ, e4 := strconv.Atoi(strings.TrimSpace(parts[3]))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return rawObject{}, false
	}
	// stable truncates positions to integers
	o := rawObject{x: math.Trunc(x), y: math.Trunc(y), time: t, typ: typ, newCombo: typ&4 != 0}
	switch {
	case typ&2 != 0:
		if len(parts) < 8 {
			return rawObject{}, false
		}
		segs := strings.Split(parts[5], "|")
		o.curve = 'B'
		o.points = []Vec{{0, 0}}
		for _, s := range segs {
			if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
				if len(o.points) == 1 {
					o.curve = s[0]
				}
				continue
			}
			px, py, ok := strings.Cut(s, ":")
			if !ok {
				continue
			}
			fx, err1 := strconv.ParseFloat(px, 64)
			fy, err2 := strconv.ParseFloat(py, 64)
			if err1 != nil || err2 != nil {
				continue
			}
			o.points = append(o.points, Vec{math.Trunc(fx) - o.x, math.Trunc(fy) - o.y})
		}
		o.slides, _ = strconv.Atoi(strings.TrimSpace(parts[6]))
		if o.slides < 1 {
			o.slides = 1
		}
		if o.slides > 9000 {
			o.slides = 9000
		}
		o.length, _ = strconv.ParseFloat(strings.TrimSpace(parts[7]), 64)
	case typ&8 != 0:
		if len(parts) >= 6 {
			o.endTime, _ = strconv.ParseFloat(strings.TrimSpace(parts[5]), 64)
		}
		if o.endTime < o.time {
			o.endTime = o.time
		}
	case typ&1 != 0:
	default:
		return rawObject{}, false // mania hold or unknown
	}
	return o, true
}

// Difficulty bundles the modded difficulty values used by the game logic.
type Difficulty struct {
	CS, AR, OD  float64
	Radius      float64 // osu!pixels
	Scale       float64
	Preempt     float64 // ms, map time
	Window300   float64
	Window100   float64
	Window50    float64
	StackOffset float64 // osu!pixels per stack level
	Rate        float64
}

func diffRange(d, min, mid, max float64) float64 {
	if d > 5 {
		return mid + (max-mid)*(d-5)/5
	}
	if d < 5 {
		return mid - (mid-min)*(5-d)/5
	}
	return mid
}

// DifficultyFor applies mods to the map's difficulty values. Values are kept
// in single precision like the game, because stacking depends on it.
func (bm *Beatmap) DifficultyFor(mods uint32) Difficulty {
	cs, ar, od := float32(bm.CS), float32(bm.AR), float32(bm.OD)
	if mods&ModHardRock != 0 {
		cs = min32(cs*1.3, 10)
		ar = min32(ar*1.4, 10)
		od = min32(od*1.4, 10)
	}
	if mods&ModEasy != 0 {
		cs *= 0.5
		ar *= 0.5
		od *= 0.5
	}
	d := Difficulty{CS: float64(cs), AR: float64(ar), OD: float64(od), Rate: RateOf(mods)}
	d.Scale = float64(float32((1 - 0.7*(float64(cs)-5)/5) / 2))
	d.Radius = 64 * d.Scale
	d.Preempt = math.Trunc(diffRange(float64(ar), 1800, 1200, 450))
	d.Window300 = diffRange(float64(od), 80, 50, 20)
	d.Window100 = diffRange(float64(od), 140, 100, 60)
	d.Window50 = diffRange(float64(od), 200, 150, 100)
	d.StackOffset = d.Scale * 6.4
	return d
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func stackThresholdFor(preempt, leniency float64) float64 {
	return float64(float32(int(preempt)) * float32(leniency))
}

// BPM returns the dominant BPM (by time covered).
func (bm *Beatmap) BPM() float64 {
	if len(bm.raw) == 0 {
		return 0
	}
	end := bm.raw[len(bm.raw)-1].time
	dur := map[float64]float64{}
	var un []TimingPoint
	for _, tp := range bm.TimingPoints {
		if tp.Uninherited && tp.BeatLength > 0 {
			un = append(un, tp)
		}
	}
	for i, tp := range un {
		next := end
		if i+1 < len(un) {
			next = un[i+1].Time
		}
		if next > tp.Time || i == len(un)-1 {
			dur[math.Round(tp.BeatLength*1000)/1000] += math.Max(next-tp.Time, 1)
		}
	}
	best, bestD := 0.0, -1.0
	for bl, d := range dur {
		if d > bestD {
			best, bestD = bl, d
		}
	}
	if best <= 0 {
		return 0
	}
	return 60000 / best
}

// LengthMs returns the drain span of the map in map time.
func (bm *Beatmap) LengthMs() float64 {
	if len(bm.raw) == 0 {
		return 0
	}
	return bm.raw[len(bm.raw)-1].time - bm.raw[0].time
}

func (bm *Beatmap) timingAt(t float64) (beatLength, sv float64) {
	beatLength = 500
	sv = 1
	found := false
	for _, tp := range bm.TimingPoints {
		if tp.Time > t+0.5 {
			break
		}
		if tp.Uninherited {
			if tp.BeatLength > 0 {
				beatLength = tp.BeatLength
				found = true
			}
			sv = 1
		} else if tp.BeatLength < 0 {
			sv = math.Min(math.Max(-100/tp.BeatLength, 0.1), 10)
		} else if tp.BeatLength > 0 {
			sv = 1
		}
	}
	if !found {
		for _, tp := range bm.TimingPoints {
			if tp.Uninherited && tp.BeatLength > 0 {
				beatLength = tp.BeatLength
				break
			}
		}
	}
	return
}

// Objects builds the playable objects for the given mods: HR flip, slider
// paths, end times and stacking, exactly as the game does.
func (bm *Beatmap) Objects(mods uint32) ([]HitObject, Difficulty) {
	d := bm.DifficultyFor(mods)
	hr := mods&ModHardRock != 0
	objs := make([]HitObject, 0, len(bm.raw))
	for i, r := range bm.raw {
		o := HitObject{Index: i, Time: r.time, EndTime: r.time, Pos: Vec{r.x, r.y}, NewCombo: r.newCombo, Slides: 1}
		switch {
		case r.typ&2 != 0:
			o.Kind = KindSlider
			pts := make([]Vec, len(r.points))
			copy(pts, r.points)
			if hr {
				for k := range pts {
					pts[k].Y = -pts[k].Y
				}
			}
			p := newSliderPath(r.curve, pts, r.length)
			o.path = p
			o.Slides = r.slides
			bl, sv := bm.timingAt(r.time)
			velocity := 100 * bm.SliderMult * sv / bl // osu!px per ms
			if velocity > 0 && !math.IsInf(velocity, 0) {
				o.EndTime = r.time + float64(r.slides)*p.distance/velocity
			}
			if r.slides%2 == 1 {
				o.EndPos = p.positionAt(p.distance)
			} else {
				o.EndPos = Vec{}
			}
		case r.typ&8 != 0:
			o.Kind = KindSpinner
			o.EndTime = r.endTime
			o.Pos = Vec{256, 192}
		default:
			o.Kind = KindCircle
		}
		if hr && o.Kind != KindSpinner {
			o.Pos.Y = 384 - o.Pos.Y
		}
		if o.Kind == KindSlider {
			o.EndPos = o.Pos.Add(o.EndPos)
		} else {
			o.EndPos = o.Pos
		}
		objs = append(objs, o)
	}
	if bm.FormatVersion >= 6 {
		applyStacking(objs, d.Preempt, bm.StackLeniency)
	} else {
		applyStackingOld(objs, d.Preempt, bm.StackLeniency)
	}
	return objs, d
}

const stackDistance = 3

func applyStacking(objs []HitObject, preempt, leniency float64) {
	n := len(objs)
	if n == 0 {
		return
	}
	for i := range objs {
		objs[i].StackHeight = 0
	}
	stackThreshold := stackThresholdFor(preempt, leniency)
	startIndex, endIndex := 0, n-1
	extendedEndIndex := endIndex
	if endIndex < n-1 {
		for i := endIndex; i >= startIndex; i-- {
			stackBaseIndex := i
			for k := stackBaseIndex + 1; k < n; k++ {
				base := &objs[stackBaseIndex]
				if base.Kind == KindSpinner {
					break
				}
				objN := &objs[k]
				if objN.Kind == KindSpinner {
					continue
				}
				if objN.Time-base.EndTime > stackThreshold {
					break
				}
				if base.Pos.Dist(objN.Pos) < stackDistance || (base.Kind == KindSlider && base.EndPos.Dist(objN.Pos) < stackDistance) {
					stackBaseIndex = k
					objN.StackHeight = 0
				}
			}
			if stackBaseIndex > extendedEndIndex {
				extendedEndIndex = stackBaseIndex
				if extendedEndIndex == n-1 {
					break
				}
			}
		}
	}
	extendedStartIndex := startIndex
	for i := extendedEndIndex; i > startIndex; i-- {
		k := i
		objI := &objs[i]
		if objI.StackHeight != 0 || objI.Kind == KindSpinner {
			continue
		}
		if objI.Kind == KindCircle {
			for {
				k--
				if k < 0 {
					break
				}
				objN := &objs[k]
				if objN.Kind == KindSpinner {
					continue
				}
				// integer truncation matches stable
				if float64(int(objI.Time)-int(objN.EndTime)) > stackThreshold {
					break
				}
				if k < extendedStartIndex {
					objN.StackHeight = 0
					extendedStartIndex = k
				}
				if objN.Kind == KindSlider && objN.EndPos.Dist(objI.Pos) < stackDistance {
					offset := objI.StackHeight - objN.StackHeight + 1
					for j := k + 1; j <= i; j++ {
						objJ := &objs[j]
						if objN.EndPos.Dist(objJ.Pos) < stackDistance {
							objJ.StackHeight -= offset
						}
					}
					break
				}
				if objN.Pos.Dist(objI.Pos) < stackDistance {
					objN.StackHeight = objI.StackHeight + 1
					objI = objN
				}
			}
		} else if objI.Kind == KindSlider {
			for {
				k--
				if k < startIndex {
					break
				}
				objN := &objs[k]
				if objN.Kind == KindSpinner {
					continue
				}
				if objI.Time-objN.Time > stackThreshold {
					break
				}
				if objN.EndPos.Dist(objI.Pos) < stackDistance {
					objN.StackHeight = objI.StackHeight + 1
					objI = objN
				}
			}
		}
	}
}

func applyStackingOld(objs []HitObject, preempt, leniency float64) {
	for i := range objs {
		cur := &objs[i]
		if cur.StackHeight != 0 && cur.Kind != KindSlider {
			continue
		}
		startTime := cur.EndTime
		sliderStack := 0
		for j := i + 1; j < len(objs); j++ {
			stackThreshold := stackThresholdFor(preempt, leniency)
			if objs[j].Time-stackThreshold > startTime {
				break
			}
			position2 := cur.Pos
			if cur.Kind == KindSlider && cur.path != nil {
				position2 = cur.Pos.Add(cur.path.positionAt(cur.path.distance))
			}
			if objs[j].Pos.Dist(cur.Pos) < stackDistance {
				cur.StackHeight++
				startTime = objs[j].Time
			} else if objs[j].Pos.Dist(position2) < stackDistance {
				sliderStack++
				objs[j].StackHeight -= sliderStack
				startTime = objs[j].Time
			}
		}
	}
}
