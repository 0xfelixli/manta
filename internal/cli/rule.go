package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/config"
)

func init() {
	ruleCmd.AddCommand(ruleListCmd, ruleAddCmd, ruleRmCmd)
	rootCmd.AddCommand(ruleCmd)
}

var ruleCmd = &cobra.Command{
	Use:   "rule",
	Short: "Manage custom routing override rules (prepended ahead of subscription rules)",
}

var ruleListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List custom override rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		if len(cfg.CustomRules) == 0 {
			fmt.Println("No custom rules defined. Add one with: manta rule add \"DOMAIN-SUFFIX,example.com,DIRECT\"")
			return nil
		}
		for i, r := range cfg.CustomRules {
			fmt.Printf("  %s %s\n", cliMuted.Render(fmt.Sprintf("%2d.", i+1)), cliVal.Render(r))
		}
		return nil
	},
}

var ruleAddCmd = &cobra.Command{
	Use:   "add <RULE>",
	Short: "Add a high-priority custom routing rule (e.g. \"DOMAIN-SUFFIX,example.com,DIRECT\")",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rule := strings.TrimSpace(args[0])
		if strings.Count(rule, ",") < 2 {
			return fmt.Errorf("invalid Clash rule %q (expected TYPE,VALUE,TARGET e.g. DOMAIN-SUFFIX,example.com,DIRECT)", rule)
		}
		paths, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}
		for _, existing := range cfg.CustomRules {
			if existing == rule {
				fmt.Printf("Rule %q already exists\n", rule)
				return nil
			}
		}
		cfg.CustomRules = append([]string{rule}, cfg.CustomRules...)
		if running, _ := mgr.IsRunning(); running {
			if live, err := client.GetConfigs(); err == nil && live.Tun.Enable {
				cfg.TunEnabled = true
			}
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("%s Added custom rule: %s\n", cliOnline.Render("✓"), cliVal.Render(rule))

		if running, _ := mgr.IsRunning(); running {
			if err := config.BuildRuntimeConfig(paths, cfg); err == nil {
				if err := client.ReloadConfig(paths.RuntimeFile); err == nil {
					fmt.Println(cliMuted.Render("  Hot-reloaded running daemon"))
				}
			}
		}
		return nil
	},
}

var ruleRmCmd = &cobra.Command{
	Use:   "rm <index-or-rule>",
	Short: "Remove a custom rule by 1-based index or exact rule string",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := strings.TrimSpace(args[0])
		paths, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}
		removeIdx := -1
		if idx, err := strconv.Atoi(target); err == nil && idx >= 1 && idx <= len(cfg.CustomRules) {
			removeIdx = idx - 1
		} else {
			for i, r := range cfg.CustomRules {
				if r == target {
					removeIdx = i
					break
				}
			}
		}
		if removeIdx < 0 {
			return fmt.Errorf("custom rule %q not found", target)
		}
		removed := cfg.CustomRules[removeIdx]
		cfg.CustomRules = append(cfg.CustomRules[:removeIdx], cfg.CustomRules[removeIdx+1:]...)
		if running, _ := mgr.IsRunning(); running {
			if live, err := client.GetConfigs(); err == nil && live.Tun.Enable {
				cfg.TunEnabled = true
			}
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("%s Removed custom rule: %s\n", cliOnline.Render("✓"), removed)

		if running, _ := mgr.IsRunning(); running {
			if err := config.BuildRuntimeConfig(paths, cfg); err == nil {
				_ = client.ReloadConfig(paths.RuntimeFile)
			}
		}
		return nil
	},
}
