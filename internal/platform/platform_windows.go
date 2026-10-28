//go:build windows

// Package platform contains the operating-system specific helpers.
package platform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	shell32                           = windows.NewLazySystemDLL("shell32.dll")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procMessageBoxW                   = user32.NewProc("MessageBoxW")
	procShellExecuteW                 = shell32.NewProc("ShellExecuteW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

// Init makes the process DPI aware so screen sizes are in real pixels.
func Init() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		procSetProcessDpiAwarenessContext.Call(uintptr(^uintptr(3)))
	}
}

// ScreenSize returns the primary monitor resolution in pixels.
func ScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(0)
	h, _, _ := procGetSystemMetrics.Call(1)
	return int(w), int(h)
}

func exeDirFromCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	var exe string
	if strings.HasPrefix(cmd, "\"") {
		if i := strings.Index(cmd[1:], "\""); i >= 0 {
			exe = cmd[1 : i+1]
		}
	} else if i := strings.Index(strings.ToLower(cmd), ".exe"); i >= 0 {
		exe = cmd[:i+4]
	}
	if exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

func dirOK(d string) bool {
	if d == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(d, "osu!.exe")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(d, "osu!.db")); err == nil {
		return true
	}
	return false
}

// FindOsuDir locates the osu! stable install folder.
func FindOsuDir() string {
	for _, key := range []string{`osu\shell\open\command`, `osustable.File.osz\shell\open\command`, `osustable.File.osr\shell\open\command`, `osu!\shell\open\command`} {
		k, err := registry.OpenKey(registry.CLASSES_ROOT, key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("")
		k.Close()
		if err == nil {
			if d := exeDirFromCommand(v); dirOK(d) {
				return d
			}
		}
	}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		if d := filepath.Join(la, "osu!"); dirOK(d) {
			return d
		}
	}
	for _, d := range []string{`C:\osu!`, `C:\Program Files\osu!`, `C:\Program Files (x86)\osu!`, `D:\osu!`, `D:\Games\osu!`} {
		if dirOK(d) {
			return d
		}
	}
	return ""
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

func findPIDs(name string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []uint32
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			out = append(out, e.ProcessID)
		}
	}
	return out
}

// ProcessDir returns the folder of a running executable, or "".
func ProcessDir(name string) string {
	for _, pid := range findPIDs(name) {
		if p := processPath(pid); p != "" {
			return filepath.Dir(p)
		}
	}
	return ""
}

// OTDWindowName is the program name of the OpenTabletDriver window.
func OTDWindowName() string { return "OpenTabletDriver.UX.Wpf.exe" }

// IsRunning reports whether a process with that executable name runs.
func IsRunning(name string) bool { return len(findPIDs(name)) > 0 }

// SystemLightTheme reports whether Windows apps use the light theme.
func SystemLightTheme() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 1
}

// Open opens a file or URL with its default application.
func Open(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	t, _ := windows.UTF16PtrFromString(target)
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(t)), 0, 0, 1)
	if r <= 32 {
		return syscall.Errno(r)
	}
	return nil
}

// Reveal opens Explorer with the file selected.
func Reveal(path string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	exe, _ := windows.UTF16PtrFromString("explorer.exe")
	args, _ := windows.UTF16PtrFromString("/select,\"" + path + "\"")
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(exe)), uintptr(unsafe.Pointer(args)), 0, 1)
	return nil
}

// MessageBox shows a native dialog.
func MessageBox(title, text string, warning bool) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	flags := uintptr(0x40) // MB_ICONINFORMATION
	if warning {
		flags = 0x30
	}
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags)
}

// RestartProcess closes every process with that executable name and starts
// it again from the same file. It reports whether one was running.
func RestartProcess(name string) (bool, error) {
	pids := findPIDs(name)
	if len(pids) == 0 {
		return false, nil
	}
	path := processPath(pids[0])
	for _, pid := range pids {
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid); err == nil {
			_ = windows.TerminateProcess(h, 0)
			_, _ = windows.WaitForSingleObject(h, 3000)
			windows.CloseHandle(h)
		}
	}
	if path == "" {
		return true, errors.New("can't find the program to reopen")
	}
	cmd := exec.Command(path)
	cmd.Dir = filepath.Dir(path)
	return true, cmd.Start()
}
