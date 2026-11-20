package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/sysproxy"
)

var sysproxyService string

func init() {
	sysproxyCmd.PersistentFlags().StringVarP(&sysproxyService, "service", "s", "", "Target macOS network service (e.g. Wi-Fi; auto-detected if empty)")
	sysproxyCmd.AddCommand(sysproxyOnCmd, sysproxyOffCmd, sysproxyStatusCmd)
	rootCmd.AddCommand(sysproxyCmd)
}

var sysproxyCmd = &cobra.Command{
	Use:   "sysproxy",
	Short: "Manage macOS system HTTP/HTTPS/SOCKS5 proxy settings",
}

var sysproxyOnCmd = &cobra.Command{
	Use:   "on",
	Short: "Enable macOS system proxy pointing to manta's local mixed-port",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		services, err := sysproxy.ResolveServices(sysproxyService)
		if err != nil {
			return err
		}
		if err := sysproxy.Enable("127.0.0.1", cfg.MixedPort, services); err != nil {
			return err
		}
		fmt.Printf("System proxy enabled on [%s] -> 127.0.0.1:%d\n", strings.Join(services, ", "), cfg.MixedPort)
		return nil
	},
}

var sysproxyOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Disable macOS system proxy",
	RunE: func(cmd *cobra.Command, args []string) error {
		services, err := sysproxy.ResolveServices(sysproxyService)
		if err != nil {
			return err
		}
		if err := sysproxy.Disable(services); err != nil {
			return err
		}
		fmt.Printf("System proxy disabled on [%s]\n", strings.Join(services, ", "))
		return nil
	},
}

var sysproxyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current macOS system proxy state",
	RunE: func(cmd *cobra.Command, args []string) error {
		services, err := sysproxy.ResolveServices(sysproxyService)
		if err != nil {
			return err
		}
		statuses, err := sysproxy.Status(services)
		if err != nil {
			return err
		}
		for _, st := range statuses {
			fmt.Printf("[%s]\n", st.Service)
			fmt.Printf("  HTTP:   %s\n", st.HTTP)
			fmt.Printf("  HTTPS:  %s\n", st.HTTPS)
			fmt.Printf("  SOCKS5: %s\n", st.SOCKS)
		}
		return nil
	},
}
