package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

// Manager controls the background proxy daemon and menu bar lifecycle.
type Manager struct {
	Paths config.Paths
	Cfg   *config.AppConfig
}

func NewManager(paths config.Paths, cfg *config.AppConfig) *Manager {
	return &Manager{Paths: paths, Cfg: cfg}
}

// FindBinary returns the active engine description.
func (m *Manager) FindBinary() (string, error) {
	if m.Cfg.CoreBin != "" {
		if _, err := os.Stat(m.Cfg.CoreBin); err == nil {
			return m.Cfg.CoreBin, nil
		}
		return "", fmt.Errorf("configured core_bin not found: %s", m.Cfg.CoreBin)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve manta executable: %w", err)
	}
	return fmt.Sprintf("%s (embedded mihomo %s)", exe, EmbeddedVersion()), nil
}

// ReadPID reads the stored PID from manta.pid.
func (m *Manager) ReadPID() (int, error) {
	data, err := os.ReadFile(m.Paths.PIDFile)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid file")
	}
	return pid, nil
}

// IsRunning checks whether the tracked daemon process is alive.
func (m *Manager) IsRunning() (bool, int) {
	pid, err := m.ReadPID()
	if err != nil {
		return false, 0
	}
	err = syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false, 0
	}
	return true, pid
}

// Start builds runtime.yaml, launches the embedded mihomo daemon in the background, and waits for its controller API.
func (m *Manager) Start() (int, error) {
	if running, pid := m.IsRunning(); running {
		return pid, fmt.Errorf("manta is already running (pid %d)", pid)
	}

	_ = os.Remove(m.Paths.ShutdownFile)

	if err := config.BuildRuntimeConfig(m.Paths, m.Cfg); err != nil {
		return 0, err
	}

	logFile, err := os.OpenFile(m.Paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	var cmd *exec.Cmd
	if m.Cfg.CoreBin != "" {
		if _, err := os.Stat(m.Cfg.CoreBin); err != nil {
			return 0, fmt.Errorf("configured core_bin not found: %s", m.Cfg.CoreBin)
		}
		cmd = exec.Command(m.Cfg.CoreBin, "-d", m.Paths.RootDir, "-f", m.Paths.RuntimeFile)
	} else {
		exe, err := os.Executable()
		if err != nil {
			return 0, fmt.Errorf("resolve manta executable: %w", err)
		}
		cmd = exec.Command(exe, "_daemon", "-d", m.Paths.RootDir, "-f", m.Paths.RuntimeFile)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start daemon: %w", err)
	}

	pid := cmd.Process.Pid
	if err := os.WriteFile(m.Paths.PIDFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("write pid file: %w", err)
	}

	client := api.NewClient(m.Cfg.Controller, m.Cfg.Secret)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Version(); err == nil {
			return pid, nil
		}
		time.Sleep(150 * time.Millisecond)
	}

	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		_ = os.Remove(m.Paths.PIDFile)
		return 0, fmt.Errorf("daemon exited unexpectedly; check logs with `manta logs`")
	}
	return pid, nil
}

// Stop terminates the running daemon process (supporting non-root shutdown of root TUN daemons
// via the shutdown sentinel file) and automatically disables macOS system proxy so the host never loses internet.
func (m *Manager) Stop() error {
	m.DisableSysProxyQuiet()

	running, pid := m.IsRunning()
	if !running {
		_ = os.Remove(m.Paths.PIDFile)
		_ = os.Remove(m.Paths.ShutdownFile)
		return errors.New("manta is not running")
	}

	// Write shutdown sentinel so embedded daemon (even root-owned TUN) shuts itself down cleanly.
	_ = os.WriteFile(m.Paths.ShutdownFile, []byte("stop\n"), 0o644)

	// Also send SIGTERM directly if we have permission.
	killErr := syscall.Kill(pid, syscall.SIGTERM)

	deadline := time.Now().Add(3500 * time.Millisecond)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err != nil && !errors.Is(err, syscall.EPERM) {
			_ = os.Remove(m.Paths.PIDFile)
			_ = os.Remove(m.Paths.ShutdownFile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if killErr != nil && errors.Is(killErr, syscall.EPERM) {
		// External root-owned mihomo binary from older run that doesn't watch manta.shutdown:
		// disable TUN via controller API so routes are restored immediately.
		client := api.NewClient(m.Cfg.Controller, m.Cfg.Secret)
		_ = client.SetTun(false)
		return fmt.Errorf("root daemon (pid %d) requires `sudo manta down` (TUN routes already disabled)", pid)
	}

	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(m.Paths.PIDFile)
	_ = os.Remove(m.Paths.ShutdownFile)
	return nil
}

// DisableSysProxyQuiet turns off macOS system proxy if currently enabled.
func (m *Manager) DisableSysProxyQuiet() {
	if runtime.GOOS != "darwin" {
		return
	}
	services, err := sysproxy.ResolveServices("")
	if err != nil || len(services) == 0 {
		return
	}
	statuses, err := sysproxy.Status(services)
	if err != nil {
		return
	}
	for _, st := range statuses {
		if strings.HasPrefix(st.HTTP, "on ") || strings.HasPrefix(st.SOCKS, "on ") {
			_ = sysproxy.Disable(services)
			return
		}
	}
}

// StopMenubar terminates the background macOS menu bar process if running.
func (m *Manager) StopMenubar() {
	data, err := os.ReadFile(m.Paths.MenubarPIDFile)
	if err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if pid != os.Getpid() {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
		}
		_ = os.Remove(m.Paths.MenubarPIDFile)
	}
}

// PowerOffAll stops the proxy daemon, turns off macOS system proxy, and stops the menu bar process.
func (m *Manager) PowerOffAll() error {
	m.DisableSysProxyQuiet()
	m.StopMenubar()
	err := m.Stop()
	if err != nil && strings.Contains(err.Error(), "not running") {
		return nil
	}
	return err
}
