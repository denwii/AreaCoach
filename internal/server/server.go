// Package server exposes the engine to the user interface over HTTP on
// the loopback interface.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/engine"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
	"areacoach/internal/store"
)

// Version is the application version.
var Version = "2.3.3"

// Server is the HTTP front-end.
type Server struct {
	eng   *engine.Engine
	token string
	web   fs.FS
	ln    net.Listener
}

// New creates a server; web holds the static files.
func New(eng *engine.Engine, web fs.FS) *Server {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Server{eng: eng, token: hex.EncodeToString(b), web: web}
}

// Start listens on a random loopback port and returns the UI URL.
func (s *Server) Start() (string, error) {
	addr := "127.0.0.1:0"
	if p := os.Getenv("AREACOACH_PORT"); p != "" {
		addr = "127.0.0.1:" + p
		s.token = "dev"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.api)
	mux.Handle("/", http.FileServer(http.FS(s.web)))
	srv := &http.Server{Handler: noCache(mux), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	return fmt.Sprintf("http://%s/?t=%s", ln.Addr().String(), s.token), nil
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	b, err := json.Marshal(v)
	if err != nil {
		// NaN or infinite values: convert them to null
		b, err = json.Marshal(jsonSafe(reflect.ValueOf(v)))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"data can't be serialised"}`))
			return
		}
	}
	_, _ = w.Write(b)
}

// jsonSafe converts a value into plain JSON-able data, turning NaN and
// infinities into null and honouring json tags (including embedded structs).
func jsonSafe(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return jsonSafe(v.Elem())
	case reflect.Struct:
		if t, ok := v.Interface().(time.Time); ok {
			return t
		}
		m := map[string]any{}
		addStruct(m, v)
		return m
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range out {
			out[i] = jsonSafe(v.Index(i))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := map[string]any{}
		it := v.MapRange()
		for it.Next() {
			out[fmt.Sprint(it.Key().Interface())] = jsonSafe(it.Value())
		}
		return out
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	default:
		return v.Interface()
	}
}

func addStruct(m map[string]any, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		fv := v.Field(i)
		if f.Anonymous && name == "" {
			ev := fv
			if ev.Kind() == reflect.Pointer {
				if ev.IsNil() {
					continue
				}
				ev = ev.Elem()
			}
			if ev.Kind() == reflect.Struct {
				addStruct(m, ev)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		if strings.Contains(opts, "omitempty") && fv.IsZero() {
			continue
		}
		m[name] = jsonSafe(fv)
	}
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	tok := r.Header.Get("X-Token")
	if tok == "" {
		tok = r.URL.Query().Get("t")
	}
	if tok != s.token {
		fail(w, http.StatusForbidden, "access denied")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	switch {
	case path == "status":
		s.status(w)
	case path == "report":
		rep, ver, busy := s.eng.Report()
		writeJSON(w, map[string]any{"report": rep, "version": ver, "busy": busy})
	case path == "plays":
		s.plays(w, r)
	case strings.HasPrefix(path, "play/"):
		s.play(w, strings.TrimPrefix(path, "play/"))
	case path == "events":
		s.events(w, r)
	case path == "tablets":
		s.tablets(w)
	case path == "calib" && r.Method == http.MethodGet:
		writeJSON(w, s.eng.CalibStatus())
	case path == "filter" && r.Method == http.MethodGet:
		writeJSON(w, s.eng.FilterStatus())
	case r.Method != http.MethodPost:
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
	case path == "settings":
		s.settings(w, r)
	case path == "epoch":
		s.epoch(w, r)
	case path == "rescan":
		s.rescan(w, r)
	case path == "profile":
		s.profile(w, r)
	case path == "apply":
		var in struct {
			Area otd.Rect `json:"area"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if err := s.eng.ApplyArea(in.Area); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "calib":
		var in struct {
			Action string  `json:"action"`
			Shape  string  `json:"shape"` // screen, fixed, free
			Ratio  float64 `json:"ratio"` // height / width, for fixed
			Start  string  `json:"start"` // current, full, custom
			Width  float64 `json:"width"` // custom: the width to start from (mm)
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Action == "start" {
			shape := in.Shape
			if shape == "" {
				shape = engine.ShapeScreen
			}
			if err := s.eng.CalibStartAdapt(shape, in.Ratio, in.Start, in.Width); err != nil {
				fail(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, s.eng.CalibStatus())
			return
		}
		if err := s.eng.CalibAction(in.Action); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, s.eng.CalibStatus())
	case path == "filter":
		var in struct {
			Action string   `json:"action"`
			Rate   *float64 `json:"rate"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Rate != nil {
			s.eng.Store().Update(func(st *store.State) { st.Settings.FilterRate = *in.Rate })
			_ = s.eng.Store().Save()
		}
		if in.Action == "run" {
			s.eng.StartFilterStudy()
		}
		writeJSON(w, s.eng.FilterStatus())
	case path == "open":
		s.open(w, r)
	case path == "otd-window":
		if err := s.eng.ReopenOTDWindow(); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	default:
		fail(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) status(w http.ResponseWriter) {
	st := s.eng.Store().State()
	_, ver, busy := s.eng.Report()
	plays := s.eng.Store().Plays()
	ok, unknown := 0, 0
	var earliest time.Time
	for _, p := range plays {
		if p.Status != "ok" {
			continue
		}
		ok++
		if store.EpochAt(st.Epochs, p.Time) == 0 {
			unknown++
			if earliest.IsZero() || p.Time.Before(earliest) {
				earliest = p.Time
			}
		}
	}
	writeJSON(w, map[string]any{
		"version":         Version,
		"env":             s.eng.Env(),
		"import":          s.eng.Import(),
		"plays":           ok,
		"unknownPlays":    unknown,
		"earliestUnknown": earliest,
		"firstRun":        st.FirstRun,
		"epochs":          st.Epochs,
		"settings":        st.Settings,
		"reportVersion":   ver,
		"reportBusy":      busy,
		"dataDir":         s.eng.Store().Dir(),
		"calib":           s.eng.CalibStatus(),
		"profiles":        s.profiles(),
		"calibResult":     st.CalibResult,
	})
}

type playRow struct {
	*store.PlayMeta
	Epoch         int  `json:"epoch"`
	Profile       int  `json:"profile"` // the grip profile it counts for
	ProfileManual bool `json:"profileManual"`
}

func (s *Server) plays(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	prof, _ := strconv.Atoi(r.URL.Query().Get("profile"))
	st := s.eng.Store().State()
	var rows []playRow
	for _, p := range s.eng.Store().Plays() {
		if p.Status != "ok" {
			continue
		}
		pp := st.PlayProfile(p)
		if prof > 0 && pp != prof {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Artist+" "+p.Version+" "+p.Creator+" "+p.ModStr), q) {
			continue
		}
		rows = append(rows, playRow{p, store.EpochAt(st.Epochs, p.Time), pp, p.Profile != 0})
	}
	total := len(rows)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	writeJSON(w, map[string]any{"total": total, "rows": rows[offset:end]})
}

func (s *Server) play(w http.ResponseWriter, idStr string) {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid id")
		return
	}
	p, ok := s.eng.Store().Play(id)
	if !ok {
		fail(w, http.StatusNotFound, "play not found")
		return
	}
	hits, err := s.eng.Store().Hits(id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "the play data can't be read")
		return
	}
	st := s.eng.Store().State()
	eid := store.EpochAt(st.Epochs, p.Time)
	var area *otd.Rect
	for _, e := range st.Epochs {
		if e.ID == eid {
			a := e.Setup.Area
			area = &a
		}
	}
	type hitOut struct {
		T  float32  `json:"t"`
		R  uint8    `json:"r"`
		K  uint8    `json:"k"`
		C  uint8    `json:"c"`
		TX float32  `json:"tx"`
		TY float32  `json:"ty"`
		EX *float32 `json:"ex"`
		EY *float32 `json:"ey"`
		DT *float32 `json:"dt"`
		RA float32  `json:"ra"`
	}
	out := make([]hitOut, 0, len(hits))
	for i := range hits {
		h := &hits[i]
		o := hitOut{T: h.Time, R: h.Result, K: h.Kind, C: analysis.Categorize(h), TX: h.TX, TY: h.TY, RA: h.Radius}
		if h.HasClick() {
			ex, ey, dt := h.CX-h.TX, h.CY-h.TY, h.CT
			o.EX, o.EY, o.DT = &ex, &ey, &dt
		}
		out = append(out, o)
	}
	writeJSON(w, map[string]any{"play": p, "epoch": eid, "area": area, "hits": out,
		"profile": st.PlayProfile(p), "profileManual": p.Profile != 0})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	ch := s.eng.Subscribe()
	defer s.eng.Unsubscribe(ch)
	fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: {}\n\n", ev)
			fl.Flush()
		case <-ping.C:
			fmt.Fprintf(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) tablets(w http.ResponseWriter) {
	names := otd.TabletNames()
	type t struct {
		Name string  `json:"name"`
		W    float64 `json:"w"`
		H    float64 `json:"h"`
	}
	out := make([]t, 0, len(names))
	for _, n := range names {
		tw, th, _ := otd.TabletSize(n)
		out = append(out, t{n, tw, th})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var in map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	var errMsg string
	rangeChanged := false
	s.eng.Store().Update(func(st *store.State) {
		set := &st.Settings
		for k, v := range in {
			switch k {
			case "osuDir":
				var x string
				_ = json.Unmarshal(v, &x)
				x = strings.TrimSpace(x)
				if x != "" {
					if _, err := os.Stat(x); err != nil {
						errMsg = "The osu! folder given doesn't exist."
						continue
					}
				}
				set.OsuDir = x
			case "otdPath":
				var x string
				_ = json.Unmarshal(v, &x)
				x = strings.TrimSpace(x)
				if x != "" {
					if st, err := os.Stat(x); err == nil && st.IsDir() {
						x = filepath.Join(x, "settings.json")
					}
					if _, err := otd.Load(x); err != nil {
						errMsg = "The file given isn't a readable OpenTabletDriver settings.json."
						continue
					}
				}
				set.OTDPath = x
			case "preferredTablet":
				_ = json.Unmarshal(v, &set.PreferredTablet)
			case "tablet":
				var t *store.TabletOverride
				_ = json.Unmarshal(v, &t)
				if t != nil && (t.W <= 0 || t.H <= 0) {
					t = nil
				}
				set.Tablet = t
			case "gameW":
				_ = json.Unmarshal(v, &set.GameW)
			case "gameH":
				_ = json.Unmarshal(v, &set.GameH)
			case "weights":
				var wts *[analysis.NumCats]float64
				_ = json.Unmarshal(v, &wts)
				set.Weights = wts
				st.LastCompromise = nil // the priorities changed: recompute from scratch
			case "maxImport":
				_ = json.Unmarshal(v, &set.MaxImport)
			case "playerName":
				_ = json.Unmarshal(v, &set.PlayerName)
			case "theme":
				_ = json.Unmarshal(v, &set.Theme)
			case "diagHold":
				_ = json.Unmarshal(v, &set.DiagHold)
			case "diagRange":
				// "110 - 150": the widths the diagnosis may use; "" for any
				var txt string
				_ = json.Unmarshal(v, &txt)
				lo, hi, err := parseRange(txt)
				if err != nil {
					errMsg = err.Error()
					continue
				}
				set.DiagMinW, set.DiagMaxW = lo, hi
				rangeChanged = true
			}
		}
	})
	_ = s.eng.Store().Save()
	if rangeChanged {
		s.eng.RangeChanged()
	}
	s.eng.Invalidate()
	s.eng.Wake()
	if errMsg != "" {
		fail(w, http.StatusBadRequest, errMsg)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// parseRange reads the widths allowed: "110 - 150", "110-150", "110 150",
// "110, 150", "from 110 to 150"; one number alone is a lower limit; ""
// means any. A comma followed by one or two digits is a decimal comma
// ("110,5").
func parseRange(txt string) (lo, hi float64, err error) {
	var nums []float64
	cur := ""
	flush := func() {
		if cur != "" {
			if v, e := strconv.ParseFloat(strings.Replace(cur, ",", ".", 1), 64); e == nil {
				nums = append(nums, v)
			}
			cur = ""
		}
	}
	rs := []rune(txt)
	digit := func(i int) bool { return i < len(rs) && rs[i] >= '0' && rs[i] <= '9' }
	for i, r := range rs {
		decComma := r == ',' && cur != "" && !strings.ContainsAny(cur, ".,") && digit(i+1) && !(digit(i+2) && digit(i+3))
		if (r >= '0' && r <= '9') || r == '.' || decComma {
			cur += string(r)
		} else {
			flush()
		}
	}
	flush()
	switch len(nums) {
	case 0:
		return 0, 0, nil
	case 1:
		lo = nums[0]
	case 2:
		lo, hi = math.Min(nums[0], nums[1]), math.Max(nums[0], nums[1])
	default:
		return 0, 0, errors.New("type two widths in mm, for example 110 - 150")
	}
	if lo < 20 || (hi > 0 && (hi > 400 || hi-lo < 5)) {
		return 0, 0, errors.New("widths don't make sense: for example 110 - 150 (mm), at least 5 mm apart")
	}
	return lo, hi, nil
}

func (s *Server) epoch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action  string    `json:"action"`
		ID      int       `json:"id"`
		From    time.Time `json:"from"`
		Adapted bool      `json:"adapted"`
		Area    otd.Rect  `json:"area"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	st := s.eng.Store()
	var err error
	switch in.Action {
	case "start":
		err = st.SetEpochStart(in.ID, in.From)
	case "adapted":
		st.SetEpochFlag(in.ID, in.Adapted)
	case "add":
		if !in.Area.Valid() {
			err = fmt.Errorf("invalid area")
		} else {
			err = st.AddPastEpoch(in.Area, in.From)
		}
	case "remove":
		err = st.RemoveEpoch(in.ID)
	default:
		err = fmt.Errorf("unknown action")
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = st.Save()
	s.eng.Invalidate()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) rescan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Full bool `json:"full"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Full {
		s.eng.Store().ResetPlays()
		_ = s.eng.Store().Save()
		s.eng.Invalidate()
	}
	s.eng.Wake()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) open(w http.ResponseWriter, r *http.Request) {
	var in struct {
		What string `json:"what"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	env := s.eng.Env()
	var target string
	switch in.What {
	case "data":
		target = s.eng.Store().Dir()
	case "osu":
		target = env.OsuDir
	case "otd":
		target = env.OTDPath
		if target != "" {
			_ = platform.Reveal(target)
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
	}
	if target == "" {
		fail(w, http.StatusNotFound, "path not available")
		return
	}
	_ = platform.Open(target)
	writeJSON(w, map[string]bool{"ok": true})
}

type profileRow struct {
	ID      int       `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Plays   int       `json:"plays"`
	Active  bool      `json:"active"`
}

func (s *Server) profiles() []profileRow {
	st := s.eng.Store().State()
	count := map[int]int{}
	for _, p := range s.eng.Store().Plays() {
		if p.Status == "ok" {
			count[st.PlayProfile(p)]++
		}
	}
	var out []profileRow
	for _, p := range st.Profiles {
		out = append(out, profileRow{ID: p.ID, Name: p.Name, Created: p.Created, Plays: count[p.ID], Active: p.ID == st.ActiveProfile})
	}
	return out
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action  string `json:"action"`
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Play    int    `json:"play"`
		Profile int    `json:"profile"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	st := s.eng.Store()
	name := strings.TrimSpace(in.Name)
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	var err error
	switch in.Action {
	case "create":
		if name == "" {
			fail(w, http.StatusBadRequest, "Type a name for the profile")
			return
		}
		id := st.CreateProfile(name)
		err = s.eng.SwitchProfile(id)
	case "switch":
		err = s.eng.SwitchProfile(in.ID)
	case "rename":
		if name == "" {
			fail(w, http.StatusBadRequest, "Type a name for the profile")
			return
		}
		err = st.RenameProfile(in.ID, name)
	case "assign":
		err = st.SetPlayProfile(in.Play, in.Profile)
	default:
		err = errors.New("unknown action")
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = st.Save()
	s.eng.Invalidate()
	s.eng.Wake()
	writeJSON(w, map[string]any{"profiles": s.profiles()})
}
