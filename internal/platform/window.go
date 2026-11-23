package platform

import (
	"os"
	"path/filepath"
	"strings"
)

// Window is the osu! window.
type Window struct {
	Title      string `json:"title"`
	X, Y, W, H int    // drawing area on the screen (px)
	Lazer      bool   `json:"lazer"`
	PID        uint32 `json:"-"`
}

// isLazerExe tells osu!lazer's osu!.exe from stable's: stable keeps its
// songs and osu!.db next to it.
func isLazerExe(path string) bool {
	if path == "" {
		return false
	}
	if strings.Contains(strings.ToLower(path), "osulazer") {
		return true
	}
	dir := filepath.Dir(path)
	for _, n := range []string{"osu!.db", "Songs"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			return false
		}
	}
	return true
}

// MapTitle is the map in an osu! window title ("Artist - Title [Difficulty]"),
// or "" when osu! shows none (menus).
func MapTitle(title string) string {
	t := strings.TrimSpace(title)
	if !strings.HasPrefix(strings.ToLower(t), "osu!") {
		return ""
	}
	i := strings.Index(t, " - ")
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(t[i+3:])
	if !strings.HasSuffix(rest, "]") || !strings.Contains(rest, " [") {
		return ""
	}
	return rest
}

var vkNames = map[string]int{
	"space": 0x20, "enter": 0x0D, "tab": 0x09, "back": 0x08, "escape": 0x1B, "capslock": 0x14,
	"leftshift": 0xA0, "rightshift": 0xA1, "leftcontrol": 0xA2, "rightcontrol": 0xA3, "leftalt": 0xA4, "rightalt": 0xA5,
	"lshift": 0xA0, "rshift": 0xA1, "lcontrol": 0xA2, "rcontrol": 0xA3, "lalt": 0xA4, "ralt": 0xA5, "shift": 0x10, "control": 0x11, "ctrl": 0x11, "alt": 0x12,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"insert": 0x2D, "delete": 0x2E, "home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
	"oemsemicolon": 0xBA, "oemplus": 0xBB, "oemcomma": 0xBC, "oemminus": 0xBD, "oemperiod": 0xBE, "oemquestion": 0xBF,
	"oemtilde": 0xC0, "oemopenbrackets": 0xDB, "oempipe": 0xDC, "oemclosebrackets": 0xDD, "oemquotes": 0xDE, "oembackslash": 0xE2,
	";": 0xBA, "=": 0xBB, ",": 0xBC, "-": 0xBD, ".": 0xBE, "/": 0xBF, "`": 0xC0, "[": 0xDB, "\\": 0xDC, "]": 0xDD, "'": 0xDE,
	"mouseleft": 0x01, "mouseright": 0x02, "mousemiddle": 0x04, "mouse4": 0x05, "mouse5": 0x06,
}

// VirtualKey turns a key name, as osu! writes it (for example "Z", "D1",
// "LeftShift", "OemComma"), into a Windows virtual-key code.
func VirtualKey(name string) (int, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return 0, false
	}
	if len(n) == 1 && (n[0] >= 'a' && n[0] <= 'z') {
		return int(n[0]-'a') + 0x41, true
	}
	if len(n) == 1 && n[0] >= '0' && n[0] <= '9' {
		return int(n[0]-'0') + 0x30, true
	}
	if len(n) == 2 && n[0] == 'd' && n[1] >= '0' && n[1] <= '9' {
		return int(n[1]-'0') + 0x30, true
	}
	if strings.HasPrefix(n, "numpad") && len(n) == 7 && n[6] >= '0' && n[6] <= '9' {
		return int(n[6]-'0') + 0x60, true
	}
	if n[0] == 'f' && len(n) <= 3 {
		k := 0
		for _, c := range n[1:] {
			if c < '0' || c > '9' {
				k = -1
				break
			}
			k = k*10 + int(c-'0')
		}
		if k >= 1 && k <= 24 {
			return 0x6F + k, true
		}
	}
	vk, ok := vkNames[n]
	return vk, ok
}
