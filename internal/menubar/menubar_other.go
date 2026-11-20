//go:build !darwin

package menubar

import (
	"fmt"
	"runtime"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
)

func Run(_ *config.AppConfig, _ *api.Client) error {
	return fmt.Errorf("manta menubar is only supported on macOS (got %s)", runtime.GOOS)
}
