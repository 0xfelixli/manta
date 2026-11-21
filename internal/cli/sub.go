package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/config"
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
		meta, err := config.FetchProfile(paths, name, subURL)
		if err != nil {
			return err
		}
		cfg.Profiles[name] = meta
		if cfg.ActiveProfile == "" {
			cfg.ActiveProfile = name
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("%s Saved profile %q (active: %s)\n", cliOnline.Render("✓"), name, cfg.ActiveProfile)
		if q := meta.QuotaSummary(); q != "" {
			fmt.Printf("  Quota: %s\n", q)
		}
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
			mark := "  "
			if name == cfg.ActiveProfile {
				mark = cliOnline.Render("● ")
			}
			meta := cfg.Profiles[name]
			quota := ""
			if q := meta.QuotaSummary(); q != "" {
				quota = "  •  " + cliAccent.Render(q)
			}
			fmt.Printf("%s%-14s %s%s\n", mark, cliVal.Render(name), cliMuted.Render("updated "+meta.UpdatedAt.Format("2006-01-02 15:04")), quota)
		}
		return nil
	},
}

func completeProfileNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, cfg, _, _, err := loadContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

var subUseCmd = &cobra.Command{
	Use:               "use <name>",
	Short:             "Switch the active profile (restarts daemon if running)",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeProfileNames,
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
	Use:               "update [name]",
	Short:             "Refresh a subscription profile (defaults to active profile)",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProfileNames,
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
		newMeta, err := config.FetchProfile(paths, name, meta.URL)
		if err != nil {
			return err
		}
		cfg.Profiles[name] = newMeta
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("%s Updated profile %q\n", cliOnline.Render("✓"), name)
		if q := newMeta.QuotaSummary(); q != "" {
			fmt.Printf("  Quota: %s\n", q)
		}
		return nil
	},
}
