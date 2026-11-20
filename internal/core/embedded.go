package core

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/metacubex/mihomo/component/updater"
	mihomoconfig "github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/log"
	"go.uber.org/automaxprocs/maxprocs"
)

const DefaultEmbeddedMihomoVersion = "1.19.34"

func init() {
	if C.Version == "" || C.Version == "unknown" {
		C.Version = DefaultEmbeddedMihomoVersion
	}
}

// EmbeddedVersion returns the version string of the built-in mihomo engine.
func EmbeddedVersion() string {
	return C.Version
}

// RunEmbeddedDaemon runs the embedded mihomo core in the current process until
// SIGINT/SIGTERM or a local manta.shutdown sentinel file appears.
func RunEmbeddedDaemon(homeDir, configFile string) error {
	_, _ = maxprocs.Set(maxprocs.Logger(func(string, ...any) {}))

	if homeDir != "" {
		if !filepath.IsAbs(homeDir) {
			cwd, _ := os.Getwd()
			homeDir = filepath.Join(cwd, homeDir)
		}
		C.SetHomeDir(homeDir)
	}

	shutdownSentinel := filepath.Join(C.Path.HomeDir(), "manta.shutdown")
	_ = os.Remove(shutdownSentinel)

	if configFile != "" {
		if !filepath.IsAbs(configFile) {
			cwd, _ := os.Getwd()
			configFile = filepath.Join(cwd, configFile)
		}
	} else {
		configFile = filepath.Join(C.Path.HomeDir(), C.Path.Config())
	}
	C.SetConfig(configFile)

	if err := mihomoconfig.Init(C.Path.HomeDir()); err != nil {
		return fmt.Errorf("init mihomo dir: %w", err)
	}

	if err := hub.Parse(nil); err != nil {
		return fmt.Errorf("parse mihomo config: %w", err)
	}

	if updater.GeoAutoUpdate() {
		updater.RegisterGeoUpdater()
	}

	defer executor.Shutdown()

	termSign := make(chan os.Signal, 1)
	hupSign := make(chan os.Signal, 1)
	signal.Notify(termSign, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(hupSign, syscall.SIGHUP)

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-termSign:
			_ = os.Remove(shutdownSentinel)
			return nil
		case <-ticker.C:
			if _, err := os.Stat(shutdownSentinel); err == nil {
				_ = os.Remove(shutdownSentinel)
				return nil
			}
		case <-hupSign:
			if err := hub.Parse(nil); err != nil {
				log.Errorln("Reload config error: %s", err.Error())
			}
		}
	}
}
