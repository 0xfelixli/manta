package cli

import (
	"github.com/spf13/cobra"

	"manta/internal/tui"
)

func init() {
	rootCmd.AddCommand(tuiCmd)
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Launch the interactive terminal dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}
		return tui.Run(cfg, mgr, client)
	},
}
