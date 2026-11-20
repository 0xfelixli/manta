package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/config"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

var (
	upWithSysProxy   bool
	upWithTun        bool
	downWithSysProxy bool
	statusWithIP     bool
)

func init() {
	upCmd.Flags().BoolVar(&upWithSysProxy, "sys", false, "Also enable macOS system proxy")
	upCmd.Flags().BoolVar(&upWithTun, "tun", false, "Enable TUN virtual interface to capture all system & terminal traffic (requires sudo)")
	downCmd.Flags().BoolVar(&downWithSysProxy, "sys", false, "Also disable macOS system proxy")
	statusCmd.Flags().BoolVar(&statusWithIP, "ip", false, "Also query current exit IP and geolocation via the proxy")
	rootCmd.AddCommand(initCmd, upCmd, downCmd, statusCmd, logsCmd)
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
		fmt.Printf("Initialized config at %s\n", paths.ConfigFile)
		fmt.Printf("Local HTTP/SOCKS5 inbound: 127.0.0.1:%d\n", cfg.MixedPort)
		fmt.Printf("Controller API:            http://%s\n", cfg.Controller)

		if bin, err := mgr.FindBinary(); err == nil {
			fmt.Printf("Detected mihomo binary:    %s\n", bin)
		} else {
			fmt.Printf("Warning: %v\n", err)
		}
		return nil
	},
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the mihomo proxy daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if upWithTun {
			if os.Geteuid() != 0 {
				return fmt.Errorf("TUN mode requires root privileges to create utun & routes; run: sudo manta up --tun")
			}
			cfg.TunEnabled = true
		} else {
			cfg.TunEnabled = false
		}
		pid, err := mgr.Start()
		if err != nil {
			return err
		}
		fmt.Printf("manta is up (pid %d)\n", pid)
		fmt.Printf("HTTP / SOCKS5 proxy: 127.0.0.1:%d\n", cfg.MixedPort)
		if cfg.TunEnabled {
			fmt.Println("TUN mode:            enabled (all system & terminal traffic captured)")
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
	Short: "Stop the mihomo proxy daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, mgr, _, err := loadContext()
		if err != nil {
			return err
		}
		if err := mgr.Stop(); err != nil {
			return err
		}
		fmt.Println("manta stopped")
		if downWithSysProxy {
			if services, err := sysproxy.ResolveServices(""); err == nil {
				if err := sysproxy.Disable(services); err == nil {
					fmt.Printf("System proxy disabled on [%s]\n", strings.Join(services, ", "))
				} else {
					fmt.Printf("Warning: failed to disable system proxy: %v\n", err)
				}
			}
		}
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
		if !running {
			fmt.Println("Status:  stopped")
			fmt.Printf("Profile: %s\n", emptyOr(cfg.ActiveProfile, "(none)"))
			return nil
		}

		fmt.Printf("Status:  running (pid %d)\n", pid)
		fmt.Printf("Profile: %s\n", emptyOr(cfg.ActiveProfile, "(default direct)"))
		fmt.Printf("Inbound: http://127.0.0.1:%d | socks5://127.0.0.1:%d\n", cfg.MixedPort, cfg.MixedPort)
		fmt.Printf("API:     http://%s\n", cfg.Controller)

		if ver, err := client.Version(); err == nil {
			fmt.Printf("Core:    mihomo %s\n", ver.Version)
		}
		if live, err := client.GetConfigs(); err == nil {
			fmt.Printf("Mode:    %s\n", live.Mode)
			if live.Tun.Enable {
				fmt.Printf("TUN:     enabled (%s)\n", emptyOr(live.Tun.Stack, "mixed"))
			} else {
				fmt.Println("TUN:     disabled")
			}
		}
		if runtime.GOOS == "darwin" {
			if services, err := sysproxy.ResolveServices(""); err == nil {
				if sts, err := sysproxy.Status(services); err == nil && len(sts) > 0 {
					fmt.Printf("SysProxy: %s [%s]\n", sts[0].HTTP, sts[0].Service)
				}
			}
		}
		if statusWithIP {
			if info, err := api.FetchExitIP(cfg.MixedPort); err == nil {
				loc := strings.TrimSpace(strings.Join([]string{info.Country, info.City}, " "))
				fmt.Printf("Exit IP: %s (%s • %s)\n", info.IP, loc, info.ISP)
			} else {
				fmt.Printf("Exit IP: lookup failed (%v)\n", err)
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
