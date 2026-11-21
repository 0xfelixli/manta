package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

const label = "com.0xfelixli.manta"

func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

// IsEnabled reports whether the macOS LaunchAgent plist is installed.
func IsEnabled() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	p, err := plistPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Enable installs a macOS LaunchAgent so manta proxy and menu bar start automatically at login.
func Enable() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launch at login is only supported on macOS")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>_autostart</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
</dict>
</plist>
`, label, exe)

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return err
	}
	return nil
}

// Disable removes the macOS LaunchAgent plist.
func Disable() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("launch at login is only supported on macOS")
	}
	p, err := plistPath()
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", p).Run()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
