//go:build linux

// Package platform contains the operating-system specific helpers.
//
// On Linux osu! stable runs under Wine and OpenTabletDriver natively. The
// size of the screen comes from X11 (XWayland on a Wayland desktop).
package platform

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Init does nothing on Linux.
func Init() {}

/* ---------- X11 ---------- */

var x struct {
	mu     sync.Mutex
	conn   *xgb.Conn
	sw, sh int
	tried  time.Time
}

// xconn connects to the X server once (again every few seconds after a
// failure). Callers hold x.mu.
func xconn() bool {
	if x.conn != nil {
		return true
	}
	if time.Since(x.tried) < 5*time.Second {
		return false
	}
	x.tried = time.Now()
	c, err := xgb.NewConn()
	if err != nil {
		return false
	}
	scr := xproto.Setup(c).DefaultScreen(c)
	x.conn = c
	x.sw, x.sh = int(scr.WidthInPixels), int(scr.HeightInPixels)
	return true
}

// ScreenSize returns the size of the X screen.
func ScreenSize() (int, int) {
	if w, _ := strconv.Atoi(os.Getenv("AREACOACH_SCREEN_W")); w > 0 {
		if h, _ := strconv.Atoi(os.Getenv("AREACOACH_SCREEN_H")); h > 0 {
			return w, h
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if xconn() && x.sw > 0 && x.sh > 0 {
		return x.sw, x.sh
	}
	return 1920, 1080
}

/* ---------- processes ---------- */

type proc struct {
	pid  int
	args []string
}

// procs lists the processes whose command line names the program (with or
// without .exe, as a Windows program under Wine or a .NET one).
func procs(name string) []proc {
	stem := strings.ToLower(strings.TrimSuffix(name, ".exe"))
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []proc
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(b) == 0 {
			continue
		}
		args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
		for _, a := range args {
			base := strings.ToLower(a)
			if i := strings.LastIndexAny(base, `/\`); i >= 0 {
				base = base[i+1:]
			}
			if base == stem || base == stem+".exe" || base == stem+".dll" {
				out = append(out, proc{pid, args})
				break
			}
		}
	}
	return out
}

// IsRunning reports whether a program runs.
func IsRunning(name string) bool { return len(procs(name)) > 0 }

// ProcessDir returns the folder of a running program, or "".
func ProcessDir(name string) string {
	for _, p := range procs(name) {
		if d := winDir(p); d != "" {
			return d
		}
		if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", p.pid)); err == nil && !strings.Contains(exe, "wine") {
			return filepath.Dir(exe)
		}
	}
	return ""
}

// winDir turns the Windows path of a program running under Wine into the
// folder on this system.
func winDir(p proc) string {
	stem := strings.ToLower(filepath.Base(strings.ReplaceAll(p.args[0], `\`, "/")))
	for _, a := range p.args {
		if strings.HasSuffix(strings.ToLower(a), ".exe") {
			stem = a
			break
		}
	}
	path := strings.ReplaceAll(stem, `\`, "/")
	if len(path) >= 2 && path[1] == ':' {
		prefix := ""
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", p.pid)); err == nil {
			for _, kv := range bytes.Split(b, []byte{0}) {
				if v, ok := strings.CutPrefix(string(kv), "WINEPREFIX="); ok {
					prefix = v
				}
			}
		}
		if prefix == "" {
			if h, err := os.UserHomeDir(); err == nil {
				prefix = filepath.Join(h, ".wine")
			}
		}
		drive := strings.ToLower(path[:1])
		rest := path[2:]
		if drive == "z" {
			path = rest
		} else {
			path = filepath.Join(prefix, "drive_"+drive, rest)
		}
		if d := filepath.Dir(path); dirOK(d) {
			return d
		}
	}
	if filepath.IsAbs(path) {
		if d := filepath.Dir(path); dirOK(d) {
			return d
		}
	}
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", p.pid)); err == nil && dirOK(cwd) {
		return cwd
	}
	return ""
}

func dirOK(d string) bool {
	if d == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(d, "osu!.db"))
	return err == nil
}

// FindOsuDir locates the osu! stable folder in the usual Wine setups.
func FindOsuDir() string {
	if d := os.Getenv("OSU_DIR"); d != "" {
		return d
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	user := os.Getenv("USER")
	cands := []string{
		filepath.Join(h, ".local/share/osu-wine/osu!"),
		filepath.Join(h, ".local/share/osu-wine/OSU"),
		filepath.Join(h, ".local/share/osu-wine/osu"),
		filepath.Join(h, ".wine/drive_c/osu!"),
		filepath.Join(h, ".wine/drive_c/users", user, "AppData/Local/osu!"),
		filepath.Join(h, ".wine-osu/drive_c/osu!"),
		filepath.Join(h, "Games/osu/drive_c/osu!"),
		filepath.Join(h, "Games/osu!/drive_c/osu!"),
		filepath.Join(h, "osu!"),
	}
	for _, d := range cands {
		if dirOK(d) {
			return d
		}
	}
	// any folder named like osu! a couple of levels down the usual places
	for _, base := range []string{filepath.Join(h, ".local/share"), filepath.Join(h, "Games")} {
		if ms, _ := filepath.Glob(filepath.Join(base, "*", "*", "osu!.db")); len(ms) > 0 {
			return filepath.Dir(ms[0])
		}
		if ms, _ := filepath.Glob(filepath.Join(base, "*", "osu!.db")); len(ms) > 0 {
			return filepath.Dir(ms[0])
		}
	}
	return ""
}

// OTDWindowName is the program name of the OpenTabletDriver window.
func OTDWindowName() string { return "OpenTabletDriver.UX.Gtk" }

// RestartProcess closes a program and starts it again with the same
// command line; it reports whether it was running.
func RestartProcess(name string) (bool, error) {
	ps := procs(name)
	if len(ps) == 0 {
		return false, nil
	}
	args := ps[0].args
	cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", ps[0].pid))
	for _, p := range ps {
		_ = syscall.Kill(p.pid, syscall.SIGTERM)
	}
	for i := 0; i < 30 && len(procs(name)) > 0; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return true, err
	}
	go func() { _ = cmd.Wait() }()
	return true, nil
}

/* ---------- desktop ---------- */

// Open opens a file, folder or address with the desktop default handler.
func Open(target string) error {
	cmd := exec.Command("xdg-open", target)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// Reveal opens the folder that holds a file.
func Reveal(path string) error { return Open(filepath.Dir(path)) }

// MessageBox shows a message with zenity or kdialog when available, and
// always prints it.
func MessageBox(title, text string, warning bool) {
	fmt.Fprintln(os.Stderr, title+": "+text)
	kind := "--info"
	if warning {
		kind = "--warning"
	}
	if p, err := exec.LookPath("zenity"); err == nil {
		_ = exec.Command(p, kind, "--title", title, "--text", text).Run()
		return
	}
	if p, err := exec.LookPath("kdialog"); err == nil {
		k := "--msgbox"
		if warning {
			k = "--sorry"
		}
		_ = exec.Command(p, "--title", title, k, text).Run()
	}
}

// SystemLightTheme is left to the browser on Linux.
func SystemLightTheme() bool { return false }
