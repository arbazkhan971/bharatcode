package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// GlobalPath returns the canonical path of the global config file
// for the current host: $XDG_CONFIG_HOME/bharatcode/config.json if
// set, else $HOME/.config/bharatcode/config.json on Unix, else
// %APPDATA%/bharatcode/config.json on Windows.
func GlobalPath() string {
	return globalPathHelper(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

// globalPathHelper is an internal helper for testing GlobalPath under different OS environments.
func globalPathHelper(goos string, getenv func(string) string, userHomeDir func() (string, error)) string {
	if goos == "windows" {
		appData := getenv("APPDATA")
		if appData == "" {
			home, err := userHomeDir()
			if err == nil {
				appData = filepath.Join(home, "AppData", "Roaming")
			}
		}
		return filepath.Join(appData, "bharatcode", "config.json")
	}

	xdg := getenv("XDG_CONFIG_HOME")
	if xdg != "" {
		return filepath.Join(xdg, "bharatcode", "config.json")
	}

	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "bharatcode", "config.json")
}

// ProjectPath returns the path of the nearest .bharatcode.json
// found by walking up from dir. Returns "" if no project file is
// found before reaching the filesystem root.
func ProjectPath(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	current := abs
	for {
		target := filepath.Join(current, ".bharatcode.json")
		if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
			return target
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}
