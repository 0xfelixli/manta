package cli

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/menubar"
)

var menubarDaemon bool

func init() {
	menubarCmd.Flags().BoolVarP(&menubarDaemon, "daemon", "d", false, "Run the menu bar app detached in the background")
	rootCmd.AddCommand(menubarCmd)
}

var menubarCmd = &cobra.Command{
	Use:   "menubar",
	Short: "Show a native macOS menu bar status icon with quick controls",
	RunE: func(cmd *cobra.Command, args []string) error {
		if menubarDaemon {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			bg := exec.Command(exe, "menubar")
			bg.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := bg.Start(); err != nil {
				return fmt.Errorf("launch menubar daemon: %w", err)
			}
			fmt.Printf("manta menu bar started in background (pid %d)\n", bg.Process.Pid)
			return nil
		}

		_, cfg, _, client, err := loadContext()
		if err != nil {
			return err
		}
		return menubar.Run(cfg, client)
	},
}
