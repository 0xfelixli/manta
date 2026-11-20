package cli

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"manta/internal/config"
)

func init() {
	subCmd.AddCommand(subAddCmd, subListCmd, subUseCmd, subUpdateCmd)
	rootCmd.AddCommand(subCmd)
}

var subCmd = &cobra.Command{
	Use:   "sub",
	Short: "Manage Clash YAML subscription profiles",
}

var subAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Download and save a subscription profile",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, subURL := args[0], args[1]
		paths, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		fmt.Printf("Fetching profile %q...\n", name)
		if err := config.FetchProfile(paths, name, subURL); err != nil {
			return err
		}
		cfg.Profiles[name] = config.ProfileMeta{
			URL:       subURL,
			UpdatedAt: time.Now(),
		}
		if cfg.ActiveProfile == "" {
			cfg.ActiveProfile = name
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("Saved profile %q (active: %s)\n", name, cfg.ActiveProfile)
		return nil
	},
}

var subListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List saved subscription profiles",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		if len(cfg.Profiles) == 0 {
			fmt.Println("No profiles yet. Add one with `manta sub add <name> <url>`.")
			return nil
		}
		names := make([]string, 0, len(cfg.Profiles))
		for name := range cfg.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			mark := " "
			if name == cfg.ActiveProfile {
				mark = "*"
			}
			meta := cfg.Profiles[name]
			fmt.Printf("%s %-16s updated %s\n", mark, name, meta.UpdatedAt.Format("2006-01-02 15:04"))
		}
		return nil
	},
}

var subUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch the active profile (restarts daemon if running)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		paths, cfg, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if _, ok := cfg.Profiles[name]; !ok {
			return fmt.Errorf("profile %q not found", name)
		}
		cfg.ActiveProfile = name
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("Active profile set to %q\n", name)

		if running, _ := mgr.IsRunning(); running {
			_ = mgr.Stop()
			pid, err := mgr.Start()
			if err != nil {
				return err
			}
			fmt.Printf("Restarted manta (pid %d)\n", pid)
		}
		return nil
	},
}

var subUpdateCmd = &cobra.Command{
	Use:   "update [name]",
	Short: "Refresh a subscription profile (defaults to active profile)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		name := cfg.ActiveProfile
		if len(args) == 1 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("no active profile specified")
		}
		meta, ok := cfg.Profiles[name]
		if !ok || meta.URL == "" {
			return fmt.Errorf("profile %q has no subscription URL", name)
		}
		fmt.Printf("Updating profile %q...\n", name)
		if err := config.FetchProfile(paths, name, meta.URL); err != nil {
			return err
		}
		meta.UpdatedAt = time.Now()
		cfg.Profiles[name] = meta
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("Updated profile %q. Restart with `manta down && manta up` to apply.\n", name)
		return nil
	},
}
