//go:build !darwin

package menubar

import (
	"fmt"
	"runtime"

	"manta/internal/api"
	"manta/internal/config"
)

func Run(_ *config.AppConfig, _ *api.Client) error {
	return fmt.Errorf("manta menubar is only supported on macOS (got %s)", runtime.GOOS)
}
