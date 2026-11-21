package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
)

var (
	nodeJSON       bool
	nodeBestRegion string
)

func init() {
	nodeCmd.PersistentFlags().BoolVar(&nodeJSON, "json", false, "Output results as structured JSON")
	nodeBestCmd.Flags().StringVarP(&nodeBestRegion, "region", "r", "", "Filter candidate nodes by region (e.g. JP, SG, US, HK, TW)")
	nodeCmd.AddCommand(nodeListCmd, nodeUseCmd, nodeTestCmd, nodeBestCmd)
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
			if nodeJSON {
				out := make([]api.ProxyInfo, 0, len(groups))
				for _, g := range groups {
					out = append(out, proxies[g])
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
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
		if nodeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(group)
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
			Name  string `json:"name"`
			Delay int    `json:"delay"`
			Alive bool   `json:"alive"`
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
				results[idx] = result{Name: n, Delay: d, Alive: d > 0}
			}(i, node)
		}
		wg.Wait()

		// Sort fastest first
		sort.SliceStable(results, func(i, j int) bool {
			ri, rj := results[i].Delay, results[j].Delay
			if ri <= 0 {
				ri = 1_000_000
			}
			if rj <= 0 {
				rj = 1_000_000
			}
			return ri < rj
		})

		if nodeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(results)
		}

		alive := 0
		for _, r := range results {
			if r.Delay > 0 {
				alive++
			}
			mark := "  "
			if r.Name == group.Now {
				mark = cliOnline.Render("● ")
			}
			fmt.Printf("%s%-38s %s\n", mark, r.Name, renderDelayCLI(r.Delay))
		}
		fmt.Printf("\n%s\n", cliMuted.Render(fmt.Sprintf("Tested %d nodes in %s (%d alive, sorted by speed)", len(results), groupName, alive)))
		return nil
	},
}

var nodeBestCmd = &cobra.Command{
	Use:               "best [group]",
	Short:             "Benchmark nodes and automatically switch to the fastest node (optional --region filter)",
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

		groupName := ""
		if len(args) == 1 {
			groupName = args[0]
		} else {
			for name, p := range proxies {
				if p.Type == "Selector" && (strings.Contains(name, "节点选择") || strings.EqualFold(name, "PROXY")) {
					groupName = name
					break
				}
			}
			if groupName == "" {
				for name, p := range proxies {
					if p.Type == "Selector" && name != "GLOBAL" {
						groupName = name
						break
					}
				}
			}
		}
		group, ok := proxies[groupName]
		if !ok {
			return fmt.Errorf("selector group %q not found", groupName)
		}
		if group.Type != "Selector" {
			return fmt.Errorf("group %q (%s) is not a manual Selector group", groupName, group.Type)
		}

		var candidates []string
		for _, n := range group.All {
			if n == "DIRECT" || n == "REJECT" {
				continue
			}
			if p, exists := proxies[n]; exists && (p.Type == "Selector" || p.Type == "URLTest" || p.Type == "Fallback") {
				continue
			}
			if nodeBestRegion != "" && !matchesRegion(n, nodeBestRegion) {
				continue
			}
			candidates = append(candidates, n)
		}
		if len(candidates) == 0 {
			return fmt.Errorf("no candidate nodes matched in group %q (region=%q)", groupName, nodeBestRegion)
		}

		var (
			mu       sync.Mutex
			wg       sync.WaitGroup
			bestNode string
			bestMs   = 1_000_000
			alive    int
		)
		sem := make(chan struct{}, 8)
		for _, n := range candidates {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				d, err := client.TestDelay(name, "http://www.gstatic.com/generate_204", 4000)
				if err == nil && d > 0 {
					mu.Lock()
					alive++
					if d < bestMs {
						bestMs = d
						bestNode = name
					}
					mu.Unlock()
				}
			}(n)
		}
		wg.Wait()

		if bestNode == "" {
			return fmt.Errorf("all %d candidate nodes in %q timed out", len(candidates), groupName)
		}
		if err := client.SelectProxy(groupName, bestNode); err != nil {
			return err
		}
		_ = client.CloseAllConnections()

		if nodeJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"group":      groupName,
				"node":       bestNode,
				"delay_ms":   bestMs,
				"tested":     len(candidates),
				"alive":      alive,
			})
		}
		fmt.Printf("%s Switched %s → %s (%s)\n", cliOnline.Render("✓"), cliAccent.Render(groupName), cliVal.Render(bestNode), renderDelayCLI(bestMs))
		return nil
	},
}

func matchesRegion(nodeName, region string) bool {
	r := strings.ToUpper(strings.TrimSpace(region))
	upper := strings.ToUpper(nodeName)
	aliases := map[string][]string{
		"JP": {"JP", "JAPAN", "日本", "东京", "大阪", "🇯🇵"},
		"SG": {"SG", "SINGAPORE", "新加坡", "狮城", "🇸🇬"},
		"US": {"US", "USA", "美国", "圣何塞", "洛杉矶", "西雅图", "🇺🇸"},
		"HK": {"HK", "HONGKONG", "香港", "🇭🇰"},
		"TW": {"TW", "TAIWAN", "台湾", "台北", "🇹🇼"},
		"KR": {"KR", "KOREA", "韩国", "首尔", "🇰🇷"},
	}
	if list, ok := aliases[r]; ok {
		for _, kw := range list {
			if strings.Contains(upper, kw) {
				return true
			}
		}
		return false
	}
	return strings.Contains(upper, r)
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
