package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	roleDesktop = "desktop" // listens for the other PC
	roleLaptop  = "laptop"  // only makes outgoing connections

	defaultPort   = 47823
	defaultHotkey = "Pause"
)

// Config is the content of settings.json, which lives next to the exe.
type Config struct {
	// "desktop" (listens) or "laptop" (connects out; never listens).
	Role string `json:"role"`
	// Global hotkey, e.g. "Pause", "Ctrl+Alt+M", "Win+Shift+F12".
	Hotkey string `json:"hotkey"`
	// Laptop only: name or IP of the desktop. Empty = find it automatically.
	DesktopAddress string `json:"desktop_address"`
	// TCP/UDP port the desktop listens on. Must match on both PCs.
	Port int `json:"port"`
	// Shared secret. Must match on both PCs.
	Key string `json:"key"`
	// Launch at sign-in (per-user, no admin rights needed).
	StartWithWindows bool `json:"start_with_windows"`
}

func defaultConfig(role string) Config {
	return Config{
		Role:             role,
		Hotkey:           defaultHotkey,
		Port:             defaultPort,
		StartWithWindows: true,
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig("")
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	// Notepad may save a UTF-8 byte-order mark; JSON doesn't allow one.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("settings.json is not valid JSON: %v", err)
	}
	cfg.Role = strings.ToLower(strings.TrimSpace(cfg.Role))
	cfg.Hotkey = strings.TrimSpace(cfg.Hotkey)
	cfg.DesktopAddress = strings.TrimSpace(cfg.DesktopAddress)
	if cfg.Role != roleDesktop && cfg.Role != roleLaptop {
		return cfg, fmt.Errorf(`"role" must be "desktop" or "laptop" (got %q)`, cfg.Role)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return cfg, fmt.Errorf(`"port" must be between 1 and 65535 (got %d)`, cfg.Port)
	}
	if cfg.Hotkey != "" {
		if _, _, err := parseHotkey(cfg.Hotkey); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\r', '\n'), 0o644)
}
