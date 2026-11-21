package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/skilldata"
)

var skillTarget string

func init() {
	skillInstallCmd.Flags().StringVar(&skillTarget, "target", "all", "Target AI agent: antigravity, claude, codex, or all")
	skillCmd.AddCommand(skillInstallCmd, skillShowCmd, skillStatusCmd)
	rootCmd.AddCommand(skillCmd)
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Install or display the bundled AI agent skill (Antigravity / Claude Code / Codex)",
}

type agentSkillTarget struct {
	Name string
	Dir  string
}

func resolveSkillTargets(home, target string) []agentSkillTarget {
	all := []agentSkillTarget{
		{Name: "Antigravity", Dir: filepath.Join(home, ".gemini", "config", "skills", "manta-proxy")},
		{Name: "Claude Code", Dir: filepath.Join(home, ".claude", "skills", "manta-proxy")},
		{Name: "Codex", Dir: filepath.Join(home, ".codex", "skills", "manta-proxy")},
	}
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "antigravity", "gemini":
		return all[:1]
	case "claude", "claude-code":
		return all[1:2]
	case "codex":
		return all[2:3]
	default:
		return all
	}
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the embedded manta-proxy AI skill to local agent skill directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targets := resolveSkillTargets(home, skillTarget)
		installed := 0

		for _, t := range targets {
			// When --target=all, install to Antigravity unconditionally and to Claude/Codex if their root config dir exists.
			if strings.EqualFold(skillTarget, "all") && t.Name != "Antigravity" {
				parentRoot := filepath.Dir(filepath.Dir(t.Dir))
				if _, err := os.Stat(parentRoot); err != nil {
					continue
				}
			}
			scriptsDir := filepath.Join(t.Dir, "scripts")
			if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", scriptsDir, err)
			}
			if err := os.WriteFile(filepath.Join(t.Dir, "SKILL.md"), []byte(skilldata.SkillMarkdown), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(scriptsDir, "check_proxy.sh"), []byte(skilldata.CheckProxyScript), 0o755); err != nil {
				return err
			}
			fmt.Printf("%s Installed %s skill → %s\n", cliOnline.Render("✓"), cliVal.Render(t.Name), cliMuted.Render(t.Dir))
			installed++
		}
		if installed == 0 {
			fmt.Println("No matching AI agent directories found. Try `manta skill install --target claude` or `--target antigravity`.")
		}
		return nil
	},
}

var skillShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the embedded manta-proxy SKILL.md runbook to stdout",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(skilldata.SkillMarkdown)
	},
}

var skillStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check where the manta-proxy AI skill is installed",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		fmt.Printf("%s  %s\n", cliBadge.Render("MANTA SKILL"), cliMuted.Render("embedded AI runbook"))
		for _, t := range resolveSkillTargets(home, "all") {
			p := filepath.Join(t.Dir, "SKILL.md")
			if _, err := os.Stat(p); err == nil {
				fmt.Printf("  %s %-12s %s\n", cliOnline.Render("●"), cliVal.Render(t.Name), cliMuted.Render(t.Dir))
			} else {
				fmt.Printf("  %s %-12s %s\n", cliMuted.Render("○"), cliMuted.Render(t.Name), cliMuted.Render("(not installed)"))
			}
		}
		return nil
	},
}
