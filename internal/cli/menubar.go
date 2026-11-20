package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
		paths, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}

		if menubarDaemon {
			mgr.StopMenubar()
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			bg := exec.Command(exe, "menubar")
			bg.Stdin = nil
			bg.Stdout = nil
			bg.Stderr = nil
			bg.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := bg.Start(); err != nil {
				return fmt.Errorf("launch menubar daemon: %w", err)
			}
			_ = os.WriteFile(paths.MenubarPIDFile, []byte(strconv.Itoa(bg.Process.Pid)+"\n"), 0o644)
			fmt.Printf("manta menu bar started in background (pid %d)\n", bg.Process.Pid)
			return nil
		}

		_ = os.WriteFile(paths.MenubarPIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
		defer os.Remove(paths.MenubarPIDFile)
		return menubar.Run(cfg, mgr, client)
	},
}
