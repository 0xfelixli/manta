package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(completionCmd)
}

var completionCmd = &cobra.Command{
	Use:       "completion [fish|zsh|bash]",
	Short:     "Install or output shell autocompletion (defaults to fish/zsh/bash)",
	Args:      cobra.ExactArgs(1),
	ValidArgs: []string{"fish", "zsh", "bash"},
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := args[0]
		isTTY := term.IsTerminal(os.Stdout.Fd())

		// When piped or called by Homebrew's generate_completions_from_executable, write raw script to stdout.
		if !isTTY {
			switch shell {
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "bash":
				return rootCmd.GenBashCompletionV2(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell %q", shell)
			}
		}

		// Interactive terminal: install directly to the standard user completions file without flooding stdout.
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		var target string
		switch shell {
		case "fish":
			target = filepath.Join(home, ".config", "fish", "completions", "manta.fish")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := rootCmd.GenFishCompletionFile(target, true); err != nil {
				return err
			}
		case "zsh":
			target = filepath.Join(home, ".zsh", "completions", "_manta")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := rootCmd.GenZshCompletionFile(target); err != nil {
				return err
			}
		case "bash":
			target = filepath.Join(home, ".local", "share", "bash-completion", "completions", "manta")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := rootCmd.GenBashCompletionFileV2(target, true); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported shell %q (expected fish, zsh, or bash)", shell)
		}

		fmt.Printf("✓ Installed %s completions to %s\n", shell, target)
		return nil
	},
}
