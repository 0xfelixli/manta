package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

func init() {
	nodeCmd.AddCommand(nodeListCmd, nodeUseCmd, nodeTestCmd)
	rootCmd.AddCommand(nodeCmd, modeCmd)
}

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Inspect, test, and switch proxy nodes",
}

func completeGroupNames(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, _, _, client, err := loadContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	proxies, err := client.GetProxies()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var groups []string
	for name, p := range proxies {
		if p.Type == "Selector" || p.Type == "URLTest" || p.Type == "Fallback" {
			groups = append(groups, name)
		}
	}
	sort.Strings(groups)
	return groups, cobra.ShellCompDirectiveNoFileComp
}

func completeGroupAndNode(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	_, _, _, client, err := loadContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	proxies, err := client.GetProxies()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 0 {
		var groups []string
		for name, p := range proxies {
			if p.Type == "Selector" {
				groups = append(groups, name)
			}
		}
		sort.Strings(groups)
		return groups, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		if g, ok := proxies[args[0]]; ok {
			return g.All, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

var nodeListCmd = &cobra.Command{
	Use:               "ls [group]",
	Short:             "List selector groups or nodes within a group",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeGroupNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		proxies, err := client.GetProxies()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			var groups []string
			for name, p := range proxies {
				if p.Type == "Selector" || p.Type == "URLTest" || p.Type == "Fallback" {
					groups = append(groups, name)
				}
			}
			sort.Strings(groups)
			if len(groups) == 0 {
				fmt.Println("No proxy groups found.")
				return nil
			}
			for _, g := range groups {
				p := proxies[g]
				kindTag := cliMuted.Render(fmt.Sprintf("[%-8s]", p.Type))
				if p.Type == "Selector" {
					kindTag = cliAccent.Render(fmt.Sprintf("[%-8s]", p.Type))
				}
				fmt.Printf("  %-20s %s  →  %s %s\n", g, kindTag, cliVal.Render(p.Now), cliMuted.Render(fmt.Sprintf("(%d)", len(p.All))))
			}
			return nil
		}

		groupName := args[0]
		group, ok := proxies[groupName]
		if !ok {
			return fmt.Errorf("group %q not found", groupName)
		}
		for _, node := range group.All {
			mark := "  "
			nameOut := node
			if node == group.Now {
				mark = cliOnline.Render("● ")
				nameOut = cliVal.Render(node)
			}
			delay := 0
			if info, exists := proxies[node]; exists && len(info.History) > 0 {
				delay = info.History[len(info.History)-1].Delay
			}
			fmt.Printf("%s%-38s %s\n", mark, nameOut, renderDelayCLI(delay))
		}
		return nil
	},
}

var nodeUseCmd = &cobra.Command{
	Use:               "use <group> <node>",
	Short:             "Switch the active node for a selector group",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeGroupAndNode,
	RunE: func(cmd *cobra.Command, args []string) error {
		group, node := args[0], args[1]
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		if err := client.SelectProxy(group, node); err != nil {
			return err
		}
		fmt.Printf("Switched %q -> %q\n", group, node)
		return nil
	},
}

var nodeTestCmd = &cobra.Command{
	Use:               "test <group>",
	Short:             "Concurrently measure latency for all nodes in a group",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeGroupNames,
	RunE: func(cmd *cobra.Command, args []string) error {
		groupName := args[0]
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		proxies, err := client.GetProxies()
		if err != nil {
			return err
		}
		group, ok := proxies[groupName]
		if !ok {
			return fmt.Errorf("group %q not found", groupName)
		}

		type result struct {
			name  string
			delay int
			err   error
		}
		results := make([]result, len(group.All))
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)

		for i, node := range group.All {
			wg.Add(1)
			go func(idx int, n string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				d, err := client.TestDelay(n, "http://www.gstatic.com/generate_204", 4000)
				if err != nil {
					d = -1
				}
				results[idx] = result{name: n, delay: d, err: err}
			}(i, node)
		}
		wg.Wait()

		// Sort fastest first
		sort.SliceStable(results, func(i, j int) bool {
			ri, rj := results[i].delay, results[j].delay
			if ri <= 0 {
				ri = 1_000_000
			}
			if rj <= 0 {
				rj = 1_000_000
			}
			return ri < rj
		})

		alive := 0
		for _, r := range results {
			if r.delay > 0 {
				alive++
			}
			mark := "  "
			if r.name == group.Now {
				mark = cliOnline.Render("● ")
			}
			fmt.Printf("%s%-38s %s\n", mark, r.name, renderDelayCLI(r.delay))
		}
		fmt.Printf("\n%s\n", cliMuted.Render(fmt.Sprintf("Tested %d nodes in %s (%d alive, sorted by speed)", len(results), groupName, alive)))
		return nil
	},
}

var modeCmd = &cobra.Command{
	Use:       "mode [rule|global|direct]",
	Short:     "Get or switch the active routing mode",
	Args:      cobra.MaximumNArgs(1),
	ValidArgs: []string{"rule", "global", "direct"},
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		if len(args) == 0 {
			live, err := client.GetConfigs()
			if err != nil {
				return err
			}
			fmt.Println(live.Mode)
			return nil
		}
		mode := strings.ToLower(args[0])
		if mode != "rule" && mode != "global" && mode != "direct" {
			return fmt.Errorf("invalid mode %q (expected rule, global, or direct)", mode)
		}
		if err := client.SetMode(mode); err != nil {
			return err
		}
		fmt.Printf("Mode switched to %s\n", mode)
		return nil
	},
}
