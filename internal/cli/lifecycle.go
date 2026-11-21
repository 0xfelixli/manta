package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/core"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

var (
	upWithSysProxy   bool
	upWithTun        bool
	downWithSysProxy bool
	statusWithIP     bool
	statusJSON       bool
	daemonHomeDir    string
	daemonConfigFile string
)

func init() {
	upCmd.Flags().BoolVar(&upWithSysProxy, "sys", false, "Also enable macOS system proxy")
	upCmd.Flags().BoolVar(&upWithTun, "tun", false, "Enable TUN virtual interface to capture all system & terminal traffic (requires sudo)")
	downCmd.Flags().BoolVar(&downWithSysProxy, "sys", false, "Also disable macOS system proxy")
	statusCmd.Flags().BoolVar(&statusWithIP, "ip", false, "Also query current exit IP and geolocation via the proxy")
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output status as structured JSON (for AI agents & scripts)")
	daemonCmd.Flags().StringVarP(&daemonHomeDir, "dir", "d", "", "mihomo home directory")
	daemonCmd.Flags().StringVarP(&daemonConfigFile, "file", "f", "", "mihomo runtime config file")
	rootCmd.AddCommand(initCmd, upCmd, downCmd, statusCmd, logsCmd, daemonCmd)
}

var daemonCmd = &cobra.Command{
	Use:    "_daemon",
	Hidden: true,
	Short:  "Internal embedded mihomo daemon runner",
	RunE: func(cmd *cobra.Command, args []string) error {
		return core.RunEmbeddedDaemon(daemonHomeDir, daemonConfigFile)
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize manta configuration directory and verify mihomo",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if err := config.Save(paths, cfg); err != nil {
			return err
		}
		fmt.Printf("%s  %s\n", cliBadge.Render("MANTA"), cliOnline.Render("✓ Initialized"))
		fmt.Println(renderKV("Config", paths.ConfigFile))
		fmt.Println(renderKV("Inbound", fmt.Sprintf("127.0.0.1:%d (HTTP + SOCKS5)", cfg.MixedPort)))
		fmt.Println(renderKV("Controller", "http://"+cfg.Controller))

		if bin, err := mgr.FindBinary(); err == nil {
			fmt.Println(renderKV("Engine", bin))
		} else {
			fmt.Println(renderKV("Engine", cliWarn.Render(err.Error())))
		}

		// Auto-install fish shell completions if ~/.config/fish exists.
		if home, err := os.UserHomeDir(); err == nil {
			fishDir := filepath.Join(home, ".config", "fish")
			if st, err := os.Stat(fishDir); err == nil && st.IsDir() {
				compDir := filepath.Join(fishDir, "completions")
				if err := os.MkdirAll(compDir, 0o755); err == nil {
					compFile := filepath.Join(compDir, "manta.fish")
					if err := rootCmd.GenFishCompletionFile(compFile, true); err == nil {
						fmt.Println(renderKV("Fish Comp", compFile))
					}
				}
			}
		}
		return nil
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the mihomo proxy daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}
		if upWithTun {
			cfg.TunEnabled = true
			_ = config.Save(paths, cfg)
			if !mgr.HasRootCapability() {
				fmt.Println("Requesting macOS administrator authorization for TUN mode...")
				pid, err := mgr.StartTunElevated()
				if err != nil {
					return err
				}
				fmt.Printf("manta is up (pid %d)\n", pid)
				fmt.Printf("HTTP / SOCKS5 proxy: 127.0.0.1:%d\n", cfg.MixedPort)
				fmt.Println("TUN mode:            enabled (all system & terminal traffic captured)")
				return nil
			}
		} else {
			cfg.TunEnabled = false
		}
		_ = config.Save(paths, cfg)

		// If a daemon is already running and the user ran `sudo manta up --tun`,
		// automatically stop the old daemon so the new root TUN daemon can take over cleanly.
		if running, _ := mgr.IsRunning(); running && upWithTun {
			_ = mgr.Stop()
		}

		pid, err := mgr.Start()
		if err != nil {
			return err
		}
		fmt.Printf("manta is up (pid %d)\n", pid)
		fmt.Printf("HTTP / SOCKS5 proxy: 127.0.0.1:%d\n", cfg.MixedPort)
		if cfg.TunEnabled {
			if live, err := client.GetConfigs(); err == nil && !live.Tun.Enable {
				fmt.Println("Warning: TUN failed to initialize; check `manta logs`")
			} else {
				fmt.Println("TUN mode:            enabled (all system & terminal traffic captured)")
			}
		} else if upWithSysProxy {
			if services, err := sysproxy.ResolveServices(""); err == nil {
				if err := sysproxy.Enable("127.0.0.1", cfg.MixedPort, services); err == nil {
					fmt.Printf("System proxy enabled on [%s]\n", strings.Join(services, ", "))
				} else {
					fmt.Printf("Warning: failed to enable system proxy: %v\n", err)
				}
			} else {
				fmt.Printf("Warning: %v\n", err)
			}
		} else {
			fmt.Printf("Tip: run `sudo manta up --tun` for full-system capture, or `eval \"$(manta env)\"` / `manta sysproxy on`.\n")
		}
		return nil
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop the proxy daemon, disable macOS system proxy, and close menu bar",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if err := mgr.PowerOffAll(); err != nil {
			return err
		}
		fmt.Println("manta stopped (daemon, system proxy, and menu bar cleaned up)")
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status, inbound ports, and active profile",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}
		running, pid := mgr.IsRunning()
		quotaStr := ""
		if cfg.ActiveProfile != "" {
			if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
				quotaStr = meta.QuotaSummary()
			}
		}

		if statusJSON {
			out := map[string]any{
				"online":      running,
				"pid":         pid,
				"profile":     cfg.ActiveProfile,
				"quota":       quotaStr,
				"mixed_port":  cfg.MixedPort,
				"controller":  cfg.Controller,
				"custom_rules": len(cfg.CustomRules),
			}
			if running {
				if ver, err := client.Version(); err == nil {
					out["version"] = ver.Version
				}
				if live, err := client.GetConfigs(); err == nil {
					out["mode"] = live.Mode
					out["tun_enabled"] = live.Tun.Enable
					out["tun_stack"] = live.Tun.Stack
					out["allow_lan"] = live.AllowLan
				}
				if statusWithIP {
					if info, err := api.FetchExitIP(cfg.MixedPort); err == nil {
						out["exit_ip"] = info
					}
				}
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		if !running {
			fmt.Printf("%s  %s\n", cliBadge.Render("MANTA"), cliOffline.Render("○ STOPPED"))
			fmt.Println(renderKV("Profile", emptyOr(cfg.ActiveProfile, "(none)")))
			if quotaStr != "" {
				fmt.Println(renderKV("Quota", quotaStr))
			}
			return nil
		}

		verStr := "embedded"
		if ver, err := client.Version(); err == nil {
			verStr = "mihomo " + ver.Version
		}
		fmt.Printf("%s  %s  %s\n", cliBadge.Render("MANTA"), cliOnline.Render("● ONLINE"), cliMuted.Render(fmt.Sprintf("(pid %d • %s)", pid, verStr)))
		profLine := cliVal.Render(emptyOr(cfg.ActiveProfile, "(default direct)"))
		if quotaStr != "" {
			profLine += "  " + cliMuted.Render("("+quotaStr+")")
		}
		fmt.Println(renderKV("Profile", profLine))
		if live, err := client.GetConfigs(); err == nil {
			fmt.Println(renderKV("Mode", cliAccent.Render(strings.ToUpper(live.Mode))))
			if live.Tun.Enable {
				fmt.Println(renderKV("TUN", cliOnline.Render("● ON")+" "+cliMuted.Render("("+emptyOr(live.Tun.Stack, "mixed")+")")))
			} else {
				fmt.Println(renderKV("TUN", cliMuted.Render("○ OFF")))
			}
		}
		if runtime.GOOS == "darwin" {
			if services, err := sysproxy.ResolveServices(""); err == nil {
				if sts, err := sysproxy.Status(services); err == nil && len(sts) > 0 {
					if strings.HasPrefix(sts[0].HTTP, "on ") {
						fmt.Println(renderKV("SysProxy", cliOnline.Render("● ON")+" "+cliMuted.Render(fmt.Sprintf("[%s • 127.0.0.1:%d]", sts[0].Service, cfg.MixedPort))))
					} else {
						fmt.Println(renderKV("SysProxy", cliMuted.Render("○ OFF ["+sts[0].Service+"]")))
					}
				}
			}
		}
		fmt.Println(renderKV("Inbound", fmt.Sprintf("http://127.0.0.1:%d  •  socks5://127.0.0.1:%d", cfg.MixedPort, cfg.MixedPort)))
		fmt.Println(renderKV("Controller", "http://"+cfg.Controller))
		if statusWithIP {
			if info, err := api.FetchExitIP(cfg.MixedPort); err == nil {
				loc := strings.TrimSpace(strings.Join([]string{info.Country, info.City}, ", "))
				fmt.Println(renderKV("Exit IP", cliVal.Render(info.IP)+" "+cliMuted.Render(fmt.Sprintf("(%s • %s)", loc, info.ISP))))
			} else {
				fmt.Println(renderKV("Exit IP", cliOffline.Render("lookup failed: "+err.Error())))
			}
		}
		return nil
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Print recent mihomo daemon logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, _, _, _, err := loadContext()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(paths.LogFile)
		if err != nil {
			return fmt.Errorf("read log file: %w", err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(lines) > 50 {
			lines = lines[len(lines)-50:]
		}
		fmt.Println(strings.Join(lines, "\n"))
		return nil
	},
}

func emptyOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
