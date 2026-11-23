//go:build windows

package platform

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm                = windows.NewLazySystemDLL("winmm.dll")
	procGetWindowTextW   = user32.NewProc("GetWindowTextW")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procClientToScreen   = user32.NewProc("ClientToScreen")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procIsIconic         = user32.NewProc("IsIconic")
	procTimeBeginPeriod  = winmm.NewProc("timeBeginPeriod")
	procTimeEndPeriod    = winmm.NewProc("timeEndPeriod")
	procPlaySoundW       = winmm.NewProc("PlaySoundW")
)

type point struct{ X, Y int32 }
type rect struct{ L, T, R, B int32 }

// the window search: one callback for the whole run (Windows keeps every
// callback made), with what it needs in these variables
var (
	enumMu    sync.Mutex
	enumPIDs  map[uint32]bool
	enumBest  Window
	enumFound bool
	enumCB    = syscall.NewCallback(enumWindow)
)

func enumWindow(h windows.HWND, _ uintptr) uintptr {
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil || !enumPIDs[pid] || !windows.IsWindowVisible(h) {
		return 1
	}
	if ic, _, _ := procIsIconic.Call(uintptr(h)); ic != 0 {
		return 1
	}
	var buf [512]uint16
	n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	title := windows.UTF16ToString(buf[:n])
	if !strings.HasPrefix(strings.ToLower(title), "osu!") {
		return 1
	}
	var r rect
	if ok, _, _ := procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r))); ok == 0 {
		return 1
	}
	var p point
	procClientToScreen.Call(uintptr(h), uintptr(unsafe.Pointer(&p)))
	w := Window{Title: title, X: int(p.X), Y: int(p.Y), W: int(r.R - r.L), H: int(r.B - r.T), PID: pid}
	if w.W < 100 || w.H < 100 {
		return 1
	}
	if !enumFound || w.W*w.H > enumBest.W*enumBest.H {
		enumBest, enumFound = w, true
	}
	return 1
}

// OsuWindow finds the window of osu! (stable or lazer): its title and where
// its drawing area is on the screen. Only window positions and titles are
// read, as any window list does.
func OsuWindow() (Window, bool) {
	pids := map[uint32]bool{}
	for _, pid := range findPIDs("osu!.exe") {
		pids[pid] = true
	}
	if len(pids) == 0 {
		return Window{}, false
	}
	enumMu.Lock()
	enumPIDs, enumBest, enumFound = pids, Window{}, false
	_ = windows.EnumWindows(enumCB, nil)
	best, found := enumBest, enumFound
	enumMu.Unlock()
	if found {
		best.Lazer = isLazerExe(processPath(best.PID))
	}
	return best, found
}

// CursorPos is where the cursor is on the screen (px).
func CursorPos() (int, int) {
	var p point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// KeyDown tells whether a key (Windows virtual-key code) is held now.
func KeyDown(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// FineTimer asks Windows for 1 ms timers (on) or gives the request back.
func FineTimer(on bool) {
	if on {
		procTimeBeginPeriod.Call(1)
	} else {
		procTimeEndPeriod.Call(1)
	}
}

var soundMu sync.Mutex
var soundBuf []byte

// PlayWav plays a WAV file held in memory, without waiting.
func PlayWav(b []byte) {
	soundMu.Lock()
	defer soundMu.Unlock()
	const sndAsync, sndNoDefault, sndMemory = 0x1, 0x2, 0x4
	soundBuf = b // kept alive while it plays
	procPlaySoundW.Call(uintptr(unsafe.Pointer(&soundBuf[0])), 0, sndAsync|sndNoDefault|sndMemory)
}
