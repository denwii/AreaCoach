package otd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Daemon talks to the running OpenTabletDriver daemon (0.6+) through its
// JSON-RPC pipe, the same channel its own settings window uses. It lets the
// calibration change the area live, without touching settings.json.
type Daemon struct {
	conn io.ReadWriteCloser
	r    *bufio.Reader
	mu   sync.Mutex
	id   int
}

// ErrNoDaemon means OpenTabletDriver is not running or does not answer.
var ErrNoDaemon = errors.New("OpenTabletDriver doesn't answer")

// DialDaemon connects to the daemon.
func DialDaemon(timeout time.Duration) (*Daemon, error) {
	type res struct {
		c   io.ReadWriteCloser
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := dialPipe("OpenTabletDriver.Daemon")
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNoDaemon, r.err)
		}
		return NewDaemon(r.c), nil
	case <-time.After(timeout):
		go func() {
			if r := <-ch; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, ErrNoDaemon
	}
}

// NewDaemon wraps an open connection (used by tests).
func NewDaemon(c io.ReadWriteCloser) *Daemon {
	return &Daemon{conn: c, r: bufio.NewReader(c)}
}

// Close ends the connection.
func (d *Daemon) Close() error { return d.conn.Close() }

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (d *Daemon) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(d.conn, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = d.conn.Write(b)
	return err
}

func (d *Daemon) read() (*rpcMsg, error) {
	n := -1
	for {
		line, err := d.r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if n >= 0 {
				break
			}
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, err
			}
		}
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return nil, err
	}
	var m rpcMsg
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// call sends a request and waits for its answer, skipping the notifications
// the daemon sends meanwhile (log messages, tablet reports).
func (d *Daemon) call(method string, params []any, timeout time.Duration) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.id++
	id := d.id
	if params == nil {
		params = []any{}
	}
	type result struct {
		raw json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		if err := d.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			done <- result{err: err}
			return
		}
		for {
			m, err := d.read()
			if err != nil {
				done <- result{err: err}
				return
			}
			if len(m.ID) == 0 || m.Method != "" {
				continue // a notification
			}
			if got, err := strconv.Atoi(string(m.ID)); err != nil || got != id {
				continue
			}
			if m.Error != nil {
				done <- result{err: fmt.Errorf("OpenTabletDriver: %s", m.Error.Message)}
				return
			}
			done <- result{raw: m.Result}
			return
		}
	}()
	select {
	case r := <-done:
		return r.raw, r.err
	case <-time.After(timeout):
		d.conn.Close()
		return nil, fmt.Errorf("%w (timeout)", ErrNoDaemon)
	}
}

// GetSettings returns the settings in use, as the daemon serialises them
// (the same layout as settings.json).
func (d *Daemon) GetSettings() (map[string]any, error) {
	raw, err := d.call("GetSettings", nil, 4*time.Second)
	if err != nil {
		return nil, err
	}
	s, err := DecodeSettings(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := s["Profiles"]; !ok {
		return nil, errors.New("OpenTabletDriver settings in an unknown format")
	}
	return s, nil
}

// SetSettings applies settings live (not saved to disk).
func (d *Daemon) SetSettings(s map[string]any) error {
	_, err := d.call("SetSettings", []any{s}, 6*time.Second)
	return err
}

// Resync asks the settings window, if open, to reload from the daemon.
func (d *Daemon) Resync() error {
	_, err := d.call("ForceResynchronize", nil, 3*time.Second)
	return err
}

// WithArea returns a copy of the settings with the area of the profile of
// the given tablet (or of the first absolute profile) replaced.
func WithArea(s map[string]any, tablet string, r Rect) (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	c, err := DecodeSettings(b)
	if err != nil {
		return nil, err
	}
	target := profileOf(c, tablet)
	if target == nil {
		return nil, fmt.Errorf("no OpenTabletDriver profile in absolute mode for the tablet %q", tablet)
	}
	am := target["AbsoluteModeSettings"].(map[string]any)
	tab, _ := am["Tablet"].(map[string]any)
	if tab == nil {
		tab = map[string]any{}
		am["Tablet"] = tab
	}
	tab["Width"], tab["Height"], tab["X"], tab["Y"], tab["Rotation"] = r.W, r.H, r.X, r.Y, r.Rot
	return c, nil
}

// DecodeSettings reads settings keeping every number exactly as written,
// so that writing them back changes nothing but the area.
func DecodeSettings(b []byte) (map[string]any, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var s map[string]any
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	return s, nil
}

// AreaOf reads the area of the profile of the given tablet.
func AreaOf(s map[string]any, tablet string) (Rect, bool) {
	pm := profileOf(s, tablet)
	if pm == nil {
		return Rect{}, false
	}
	am, _ := pm["AbsoluteModeSettings"].(map[string]any)
	r := rect(am["Tablet"])
	return r, r.Valid()
}

// profileOf finds the profile of a tablet among those in absolute mode: the
// one with its name, or the only one there is (never another tablet's).
func profileOf(s map[string]any, tablet string) map[string]any {
	profs, _ := s["Profiles"].([]any)
	var only map[string]any
	n := 0
	for _, p := range profs {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := pm["AbsoluteModeSettings"].(map[string]any); !ok {
			continue
		}
		if str(pm["Tablet"]) == tablet {
			return pm
		}
		only = pm
		n++
	}
	if n == 1 {
		return only
	}
	return nil
}

// Tablets returns the names of the tablets connected now.
func (d *Daemon) Tablets() ([]string, error) {
	raw, err := d.call("GetTablets", nil, 3*time.Second)
	if err != nil {
		return nil, err
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	out := []string{}
	for _, t := range list {
		props, _ := t["Properties"].(map[string]any)
		if props == nil {
			props, _ = t["properties"].(map[string]any)
		}
		name := str(props["Name"])
		if name == "" {
			name = str(props["name"])
		}
		if name != "" {
			out = append(out, name)
		}
	}
	return out, nil
}

// ErrNoRadialFollow means the profile has no Radial Follow filter to set:
// the plugin has to be installed and added once from OpenTabletDriver.
var ErrNoRadialFollow = errors.New("the OpenTabletDriver profile has no Radial Follow filter")

// RadialSettings are the values to give AbstractQbit's Radial Follow, with
// the radii in tablet millimetres.
type RadialSettings struct {
	Outer, Inner, Coef, Knee, Leak float64
}

// WithRadialFollow returns a copy of the settings with the Radial Follow
// filter of the profile of the given tablet set to r and turned on (or
// turned off when on is false). Radii are converted to pixels for the
// screen-space version of the filter, with pxPerMM.
func WithRadialFollow(s map[string]any, tablet string, r RadialSettings, on bool, pxPerMM float64) (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	c, err := DecodeSettings(b)
	if err != nil {
		return nil, err
	}
	target := profileOf(c, tablet)
	if target == nil {
		return nil, fmt.Errorf("no OpenTabletDriver profile in absolute mode for the tablet %q", tablet)
	}
	list, _ := target["Filters"].([]any)
	// the tablet-space version first, the screen-space one otherwise
	var pick map[string]any
	for _, want := range []string{"tabletspace", ""} {
		for _, it := range list {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			l := strings.ToLower(str(m["Path"]))
			if strings.Contains(l, "radialfollow") && strings.Contains(l, want) {
				pick = m
				break
			}
		}
		if pick != nil {
			break
		}
	}
	if pick == nil {
		if !on {
			return c, nil // nothing to turn off
		}
		return nil, ErrNoRadialFollow
	}
	// every other Radial Follow off: one is enough
	for _, it := range list {
		if m, ok := it.(map[string]any); ok && strings.Contains(strings.ToLower(str(m["Path"])), "radialfollow") {
			m["Enable"] = false
		}
	}
	if !on {
		return c, nil
	}
	pick["Enable"] = true
	k := 1.0
	if !strings.Contains(strings.ToLower(str(pick["Path"])), "tabletspace") && pxPerMM > 0 {
		k = pxPerMM
	}
	vals := map[string]float64{"OuterRadius": r.Outer * k, "InnerRadius": r.Inner * k, "SmoothingCoefficient": r.Coef,
		"SoftKneeScale": r.Knee, "SmoothingLeakCoefficient": r.Leak}
	ss, _ := pick["Settings"].([]any)
	seen := map[string]bool{}
	for _, x := range ss {
		if sm, ok := x.(map[string]any); ok {
			if v, ok := vals[str(sm["Property"])]; ok {
				sm["Value"] = v
				seen[str(sm["Property"])] = true
			}
		}
	}
	for _, name := range []string{"OuterRadius", "InnerRadius", "SmoothingCoefficient", "SoftKneeScale", "SmoothingLeakCoefficient"} {
		if !seen[name] {
			ss = append(ss, map[string]any{"Property": name, "Value": vals[name]})
		}
	}
	pick["Settings"] = ss
	return c, nil
}

// RadialFollowPath is the plugin type of AbstractQbit's Radial Follow
// Smoothing (Tablet coordinates).
const RadialFollowPath = "RadialFollow.RadialFollowSmoothingTabletSpace"

// AddRadialFollow returns a copy of the settings with Radial Follow (Tablet
// coordinates, off) among the filters of the profile of the tablet, for when
// the plugin is installed but was never added to the profile.
func AddRadialFollow(s map[string]any, tablet string) (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	c, err := DecodeSettings(b)
	if err != nil {
		return nil, err
	}
	target := profileOf(c, tablet)
	if target == nil {
		return nil, fmt.Errorf("no OpenTabletDriver profile in absolute mode for the tablet %q", tablet)
	}
	list, _ := target["Filters"].([]any)
	target["Filters"] = append(list, map[string]any{"Path": RadialFollowPath, "Settings": []any{}, "Enable": false})
	return c, nil
}

// RadialFollowInstalled tells whether the Radial Follow plugin is in the
// plugin folder next to the settings file of OpenTabletDriver.
func RadialFollowInstalled(settingsPath string) bool {
	if settingsPath == "" {
		return false
	}
	root := filepath.Join(filepath.Dir(settingsPath), "Plugins")
	found := false
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if found {
			return fs.SkipAll
		}
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() && strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) > 3 {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "RadialFollow.dll") {
			found = true
		}
		return nil
	})
	return found
}
