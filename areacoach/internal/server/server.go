// Package server shows the program to the interface over HTTP on the
// loopback interface.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"areacoach/internal/app"
	"areacoach/internal/live"
	"areacoach/internal/platform"
)

// Version is the program version (set when building).
var Version = "3.1.0"

// Server is the HTTP front end.
type Server struct {
	a     *app.App
	dir   string
	token string
	web   fs.FS
}

// New makes a server; web holds the interface files, dir the data folder.
func New(a *app.App, web fs.FS, dir string) *Server {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return &Server{a: a, dir: dir, token: hex.EncodeToString(b), web: web}
}

// Start listens on a free loopback port and returns the address of the
// interface.
func (s *Server) Start() (string, error) {
	addr := "127.0.0.1:0"
	if p := os.Getenv("AREACOACH_PORT"); p != "" {
		addr, s.token = "127.0.0.1:"+p, "dev"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
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
	_ = json.NewEncoder(w).Encode(v)
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
	if path == "state" {
		writeJSON(w, s.a.State())
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	var err error
	switch path {
	case "settings":
		err = s.settings(body)
	case "analyze":
		err = s.a.AnalyzeLatest()
	case "apply":
		var q struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(body, &q)
		err = s.a.ApplySuggestion(q.ID)
	case "undo":
		err = s.a.Undo()
	case "reopen-otd":
		err = s.a.ReopenOTD()
	case "live":
		var q struct {
			On bool `json:"on"`
		}
		_ = json.Unmarshal(body, &q)
		s.a.SetLive(q.On)
	case "live-back":
		err = s.a.LiveBack()
	case "forget":
		s.a.Forget()
	case "sound":
		platform.PlayWav(live.SoundShrink)
		go func() {
			time.Sleep(400 * time.Millisecond)
			platform.PlayWav(live.SoundGrow)
		}()
	case "open-data":
		err = platform.Open(s.dir)
	default:
		fail(w, http.StatusNotFound, "unknown request")
		return
	}
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, s.a.State())
}

// settings changes the settings named in the request.
func (s *Server) settings(body []byte) error {
	var q map[string]json.RawMessage
	if err := json.Unmarshal(body, &q); err != nil {
		return fmt.Errorf("bad request")
	}
	var bad error
	str := func(k string, dst *string) {
		if v, ok := q[k]; ok {
			if json.Unmarshal(v, dst) != nil {
				bad = fmt.Errorf("bad %s", k)
			}
		}
	}
	num := func(k string, dst *float64) {
		if v, ok := q[k]; ok {
			var f float64
			if string(v) == "null" || string(v) == `""` {
				*dst = 0
			} else if json.Unmarshal(v, &f) == nil {
				*dst = f
			} else {
				bad = fmt.Errorf("bad %s", k)
			}
		}
	}
	_, err := s.a.UpdateSettings(func(st *app.Settings) {
		str("osuDir", &st.OsuDir)
		str("lazerDir", &st.LazerDir)
		str("otdPath", &st.OTDPath)
		str("tablet", &st.Tablet)
		str("keys", &st.Keys)
		str("penTip", &st.PenTip)
		str("strength", &st.Strength)
		num("ratio", &st.Ratio)
		num("minW", &st.MinW)
		num("maxW", &st.MaxW)
		if v, ok := q["sound"]; ok {
			_ = json.Unmarshal(v, &st.Sound)
		}
	})
	if bad != nil {
		return bad
	}
	return err
}
