//go:build windows

package main

import (
	"path/filepath"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"areacoach/internal/platform"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	dwmapi                  = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	procFindWindowW         = user32.NewProc("FindWindowW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procDwmSetWindowAttr    = dwmapi.NewProc("DwmSetWindowAttribute")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
)

const windowTitle = "Area Coach"

func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString("AreaCoach.SingleInstance.v1")
	h, _, err := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h != 0 && err == windows.ERROR_ALREADY_EXISTS {
		cls, _ := windows.UTF16PtrFromString("webview")
		title, _ := windows.UTF16PtrFromString(windowTitle)
		if hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title))); hwnd != 0 {
			procShowWindow.Call(hwnd, 9) // SW_RESTORE
			procSetForegroundWindow.Call(hwnd)
		}
		return false
	}
	return true
}

func showUI(url, dataDir, theme string) {
	sw, sh := platform.ScreenSize()
	scale := 1.0
	if procGetDpiForSystem.Find() == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi > 0 {
			scale = float64(dpi) / 96
		}
	}
	w := int(1320 * scale)
	h := int(860 * scale)
	if w > sw*92/100 {
		w = sw * 92 / 100
	}
	if h > sh*90/100 {
		h = sh * 90 / 100
	}
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(dataDir, "webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  windowTitle,
			Width:  uint(w),
			Height: uint(h),
			IconId: 2,
			Center: true,
		},
	})
	if wv == nil {
		// WebView2 runtime missing: use the default browser instead.
		_ = platform.Open(url)
		platform.MessageBox(windowTitle, "Area Coach is open in the browser.\n\nKeep this window open: closing it stops the program.\n(For the built-in window, install the Microsoft Edge WebView2 Runtime.)", false)
		return
	}
	defer wv.Destroy()
	wv.SetSize(int(960*scale), int(640*scale), webview2.HintMin)
	if hwnd := uintptr(wv.Window()); hwnd != 0 && procDwmSetWindowAttr.Find() == nil {
		light := theme == "light" || (theme == "" && platform.SystemLightTheme())
		dark := int32(1)
		caption := uint32(0x00281E1A) // COLORREF of #1A1E28
		if light {
			dark = 0
			caption = 0x00F5F1EE // #EEF1F5
		}
		procDwmSetWindowAttr.Call(hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4) // immersive dark mode
		procDwmSetWindowAttr.Call(hwnd, 35, uintptr(unsafe.Pointer(&caption)), 4)
	}
	wv.Navigate(url)
	wv.Run()
}
