package cli

import (
	"github.com/spf13/cobra"

	"manta/internal/api"
	"manta/internal/config"
	"manta/internal/core"
)

var rootCmd = &cobra.Command{
	Use:           "manta",
	Short:         "Manta — a sleek CLI proxy manager powered by mihomo",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root CLI command.
func Execute() error {
	return rootCmd.Execute()
}

func loadContext() (config.Paths, *config.AppConfig, *core.Manager, *api.Client, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return config.Paths{}, nil, nil, nil, err
	}
	cfg, err := config.Load(paths)
	if err != nil {
		return config.Paths{}, nil, nil, nil, err
	}
	mgr := core.NewManager(paths, cfg)
	client := api.NewClient(cfg.Controller, cfg.Secret)
	return paths, cfg, mgr, client, nil
}
