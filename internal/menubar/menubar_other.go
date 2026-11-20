//go:build !darwin

package menubar

import (
	"fmt"
	"runtime"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/core"
)

func Run(_ *config.AppConfig, _ *core.Manager, _ *api.Client) error {
	return fmt.Errorf("manta menubar is only supported on macOS (got %s)", runtime.GOOS)
}
