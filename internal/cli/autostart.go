package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/autostart"
)

func init() {
	autostartCmd.AddCommand(autostartOnCmd, autostartOffCmd, autostartStatusCmd)
	rootCmd.AddCommand(autostartCmd, internalAutostartCmd)
}

var autostartCmd = &cobra.Command{
	Use:   "autostart",
	Short: "Manage macOS Launch at Login (auto-start proxy & menu bar)",
}

var autostartOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Enable macOS Launch at Login",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := autostart.Enable(); err != nil {
			return err
		}
		fmt.Printf("%s Launch at Login enabled (~/Library/LaunchAgents/com.0xfelixli.manta.plist)\n", cliOnline.Render("✓"))
		return nil
	},
}

var autostartOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Disable macOS Launch at Login",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := autostart.Disable(); err != nil {
			return err
		}
		fmt.Printf("%s Launch at Login disabled\n", cliOnline.Render("✓"))
		return nil
	},
}

var autostartStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether Launch at Login is enabled",
	RunE: func(cmd *cobra.Command, args []string) error {
		if autostart.IsEnabled() {
			fmt.Println("Launch at Login: enabled")
		} else {
			fmt.Println("Launch at Login: disabled")
		}
		return nil
	},
}

var internalAutostartCmd = &cobra.Command{
	Use:    "_autostart",
	Hidden: true,
	Short:  "Internal LaunchAgent entry point",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if running, _ := mgr.IsRunning(); !running {
			_, _ = mgr.Start()
		}
		menubarDaemon = true
		return menubarCmd.RunE(cmd, nil)
	},
}
