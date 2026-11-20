package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
)

// Manager controls the mihomo background process lifecycle.
type Manager struct {
	Paths config.Paths
	Cfg   *config.AppConfig
}

func NewManager(paths config.Paths, cfg *config.AppConfig) *Manager {
	return &Manager{Paths: paths, Cfg: cfg}
}

// FindBinary locates the mihomo executable.
func (m *Manager) FindBinary() (string, error) {
	if m.Cfg.CoreBin != "" {
		if _, err := os.Stat(m.Cfg.CoreBin); err == nil {
			return m.Cfg.CoreBin, nil
		}
		return "", fmt.Errorf("configured core_bin not found: %s", m.Cfg.CoreBin)
	}

	if path, err := exec.LookPath("mihomo"); err == nil {
		return path, nil
	}

	candidates := []string{
		"/opt/homebrew/bin/mihomo",
		"/usr/local/bin/mihomo",
		filepath.Join(m.Paths.RootDir, "bin", "mihomo"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}

	return "", errors.New("mihomo binary not found (install via `brew install mihomo` or set core_bin in config.yaml)")
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

// IsRunning checks whether the tracked mihomo process is alive.
// Note: when mihomo was started with sudo (TUN mode), syscall.Kill(pid, 0)
// from a non-root user returns EPERM, which still means the process exists.
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

// Start builds runtime.yaml, launches mihomo in the background, and waits for its controller API.
func (m *Manager) Start() (int, error) {
	if running, pid := m.IsRunning(); running {
		return pid, fmt.Errorf("manta is already running (pid %d)", pid)
	}

	bin, err := m.FindBinary()
	if err != nil {
		return 0, err
	}

	if err := config.BuildRuntimeConfig(m.Paths, m.Cfg); err != nil {
		return 0, err
	}

	logFile, err := os.OpenFile(m.Paths.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(bin, "-d", m.Paths.RootDir, "-f", m.Paths.RuntimeFile)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start mihomo: %w", err)
	}

	pid := cmd.Process.Pid
	if err := os.WriteFile(m.Paths.PIDFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		_ = cmd.Process.Kill()
		return 0, fmt.Errorf("write pid file: %w", err)
	}

	// Wait briefly for External Controller to become ready.
	client := api.NewClient(m.Cfg.Controller, m.Cfg.Secret)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := client.Version(); err == nil {
			return pid, nil
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Process started, check if it exited early.
	if err := syscall.Kill(pid, 0); err != nil {
		_ = os.Remove(m.Paths.PIDFile)
		return 0, fmt.Errorf("mihomo exited unexpectedly; check logs with `manta logs`")
	}
	return pid, nil
}

// Stop terminates the running mihomo process and removes the PID file.
func (m *Manager) Stop() error {
	running, pid := m.IsRunning()
	if !running {
		_ = os.Remove(m.Paths.PIDFile)
		return errors.New("manta is not running")
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		if errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("permission denied stopping pid %d (started with sudo/TUN); run: sudo manta down", pid)
		}
		return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			_ = os.Remove(m.Paths.PIDFile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(m.Paths.PIDFile)
	return nil
}
