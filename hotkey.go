package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Modifier flags for RegisterHotKey.
const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000
)

var namedKeys = map[string]uint32{
	"PAUSE": 0x13, "BREAK": 0x13, "PAUSEBREAK": 0x13,
	"SCROLLLOCK": 0x91, "PRINTSCREEN": 0x2C, "PRTSC": 0x2C,
	"INSERT": 0x2D, "INS": 0x2D, "DELETE": 0x2E, "DEL": 0x2E,
	"HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PGUP": 0x21, "PAGEDOWN": 0x22, "PGDN": 0x22,
	"LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28,
	"SPACE": 0x20, "TAB": 0x09, "BACKSPACE": 0x08, "ESC": 0x1B, "ESCAPE": 0x1B,
	"`": 0xC0, "BACKTICK": 0xC0, "TILDE": 0xC0,
	"-": 0xBD, "MINUS": 0xBD, "=": 0xBB, "EQUALS": 0xBB,
	"[": 0xDB, "]": 0xDD, "\\": 0xDC, ";": 0xBA, "'": 0xDE, ",": 0xBC, ".": 0xBE, "/": 0xBF,
}

// parseHotkey turns "Ctrl+Alt+M" into RegisterHotKey modifiers and a virtual-key code.
func parseHotkey(s string) (mods uint32, vk uint32, err error) {
	parts := strings.Split(s, "+")
	// A trailing "+" key ("Ctrl++") is not supported; "=" covers that key.
	for i, raw := range parts {
		p := strings.ToUpper(strings.TrimSpace(raw))
		if p == "" {
			return 0, 0, fmt.Errorf("hotkey %q is not valid", s)
		}
		last := i == len(parts)-1
		switch p {
		case "CTRL", "CONTROL":
			mods |= modControl
			continue
		case "ALT":
			mods |= modAlt
			continue
		case "SHIFT":
			mods |= modShift
			continue
		case "WIN", "WINDOWS", "SUPER":
			mods |= modWin
			continue
		}
		if !last || vk != 0 {
			return 0, 0, fmt.Errorf("hotkey %q is not valid: %q must be a modifier (Ctrl, Alt, Shift, Win)", s, raw)
		}
		switch {
		case len(p) == 1 && p[0] >= 'A' && p[0] <= 'Z', len(p) == 1 && p[0] >= '0' && p[0] <= '9':
			vk = uint32(p[0])
		case len(p) >= 2 && p[0] == 'F' && isDigits(p[1:]):
			n, _ := strconv.Atoi(p[1:])
			if n < 1 || n > 24 {
				return 0, 0, fmt.Errorf("hotkey %q is not valid: no such key %q", s, raw)
			}
			vk = 0x70 + uint32(n-1)
		case strings.HasPrefix(p, "NUMPAD") && len(p) == 7 && p[6] >= '0' && p[6] <= '9':
			vk = 0x60 + uint32(p[6]-'0')
		default:
			k, ok := namedKeys[strings.ReplaceAll(p, " ", "")]
			if !ok {
				return 0, 0, fmt.Errorf("hotkey %q is not valid: unknown key %q", s, raw)
			}
			vk = k
		}
	}
	if vk == 0 {
		return 0, 0, fmt.Errorf("hotkey %q is not valid: it needs a key, not only modifiers", s)
	}
	return mods, vk, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
