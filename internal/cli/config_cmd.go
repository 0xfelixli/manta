package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/config"
)

func init() {
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect or update saved manta preferences",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		fmt.Printf("Config file:    %s\n", paths.ConfigFile)
		fmt.Printf("mixed_port:     %d\n", cfg.MixedPort)
		fmt.Printf("mode:           %s\n", cfg.Mode)
		fmt.Printf("allow_lan:      %v\n", cfg.AllowLan)
		fmt.Printf("tun_enabled:    %v\n", cfg.TunEnabled)
		fmt.Printf("sort_by_speed:  %v\n", cfg.SortBySpeed)
		fmt.Printf("last_tab:       %s\n", emptyOr(cfg.LastTab, "dashboard"))
		fmt.Printf("last_group:     %s\n", emptyOr(cfg.LastGroup, "(auto)"))
		fmt.Printf("controller:     %s\n", cfg.Controller)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Persist a preference (sort_by_speed, mode, allow_lan, mixed_port, last_tab)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.ToLower(strings.TrimSpace(args[0]))
		val := strings.TrimSpace(args[1])

		paths, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}

		switch key {
		case "sort_by_speed":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("invalid bool %q (expected true/false)", val)
			}
			cfg.SortBySpeed = b
		case "allow_lan":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return fmt.Errorf("invalid bool %q (expected true/false)", val)
			}
			cfg.AllowLan = b
		case "mode":
			m := strings.ToLower(val)
			if m != "rule" && m != "global" && m != "direct" {
				return fmt.Errorf("invalid mode %q (expected rule, global, or direct)", val)
			}
			cfg.Mode = m
		case "mixed_port":
			p, err := strconv.Atoi(val)
			if err != nil || p < 1 || p > 65535 {
				return fmt.Errorf("invalid port %q", val)
			}
			cfg.MixedPort = p
		case "last_tab":
			t := strings.ToLower(val)
			if t != "dashboard" && t != "proxies" {
				return fmt.Errorf("invalid tab %q (expected dashboard or proxies)", val)
			}
			cfg.LastTab = t
		default:
			return fmt.Errorf("unknown setting %q (supported: sort_by_speed, mode, allow_lan, mixed_port, last_tab)", key)
		}

		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("Saved %s = %s\n", key, val)
		return nil
	},
}
