package cli

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
	"github.com/0xfelixli/manta/internal/sysproxy"
)

var (
	doctorFix  bool
	doctorJSON bool
)

func init() {
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Automatically repair common issues (stale PID, LAN DNS bypassing TUN)")
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output diagnostic report as structured JSON")
	rootCmd.AddCommand(doctorCmd)
}

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "warn", "fail"
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
	Fixed  bool   `json:"fixed,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run end-to-end proxy, TUN, DNS, and connectivity diagnostics",
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}

		var checks []doctorCheck

		// 1. Daemon & Controller API
		running, pid := mgr.IsRunning()
		var liveCfg *api.ConfigsResp
		if !running {
			c := doctorCheck{
				Name:   "Daemon & Controller",
				Status: "fail",
				Detail: "manta daemon is not running",
				Hint:   "Run `manta up --tun` or `manta up --sys`",
			}
			if doctorFix {
				if _, err := os.Stat(paths.PIDFile); err == nil {
					_ = os.Remove(paths.PIDFile)
					c.Detail += " (cleaned stale PID file)"
					c.Fixed = true
				}
			}
			checks = append(checks, c)
		} else {
			ver, vErr := client.Version()
			cfgResp, cErr := client.GetConfigs()
			if vErr != nil || cErr != nil {
				checks = append(checks, doctorCheck{
					Name:   "Daemon & Controller",
					Status: "fail",
					Detail: fmt.Sprintf("pid %d alive, but controller %s unreachable", pid, cfg.Controller),
					Hint:   "Check `manta logs` or restart with `manta down && manta up --tun`",
				})
			} else {
				liveCfg = cfgResp
				checks = append(checks, doctorCheck{
					Name:   "Daemon & Controller",
					Status: "ok",
					Detail: fmt.Sprintf("online (pid %d • mihomo %s • mode %s)", pid, ver.Version, strings.ToUpper(cfgResp.Mode)),
				})
			}
		}

		// 2. Mixed Inbound Port
		inboundAddr := fmt.Sprintf("127.0.0.1:%d", cfg.MixedPort)
		if conn, err := net.DialTimeout("tcp", inboundAddr, 1*time.Second); err == nil {
			_ = conn.Close()
			checks = append(checks, doctorCheck{
				Name:   "Inbound Proxy Port",
				Status: "ok",
				Detail: fmt.Sprintf("listening on %s (HTTP + SOCKS5)", inboundAddr),
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:   "Inbound Proxy Port",
				Status: "fail",
				Detail: fmt.Sprintf("%s is not accepting connections", inboundAddr),
				Hint:   "Ensure manta is running and port is not blocked",
			})
		}

		// 3. Traffic Capture (TUN / System Proxy)
		tunActive := liveCfg != nil && liveCfg.Tun.Enable
		sysActive := sysproxy.FastIsEnabled()
		switch {
		case tunActive && sysActive:
			checks = append(checks, doctorCheck{
				Name:   "Traffic Capture",
				Status: "ok",
				Detail: fmt.Sprintf("TUN (%s) + macOS System Proxy both active", emptyOr(liveCfg.Tun.Stack, "mixed")),
			})
		case tunActive:
			checks = append(checks, doctorCheck{
				Name:   "Traffic Capture",
				Status: "ok",
				Detail: fmt.Sprintf("TUN (%s) capturing all system & terminal traffic", emptyOr(liveCfg.Tun.Stack, "mixed")),
			})
		case sysActive:
			checks = append(checks, doctorCheck{
				Name:   "Traffic Capture",
				Status: "ok",
				Detail: "macOS System Proxy active (browser/apps proxied; use `manta up --tun` for terminal)",
			})
		default:
			checks = append(checks, doctorCheck{
				Name:   "Traffic Capture",
				Status: "warn",
				Detail: "neither TUN nor macOS System Proxy is enabled",
				Hint:   "Run `manta up --tun` or `manta sysproxy on`",
			})
		}

		// 4. macOS DNS & Fake-IP Hijack Check (when TUN is active)
		if runtime.GOOS == "darwin" && tunActive {
			dnsOut, _ := exec.Command("scutil", "--dns").Output()
			dnsText := string(dnsOut)
			lanDNSBypass := false
			for _, line := range strings.Split(dnsText, "\n") {
				l := strings.TrimSpace(line)
				if strings.HasPrefix(l, "nameserver[0]") &&
					(strings.Contains(l, "192.168.") || strings.Contains(l, "10.") || strings.Contains(l, "fe80:")) {
					lanDNSBypass = true
					break
				}
			}

			if lanDNSBypass && doctorFix {
				sysproxy.EnsureTunDNS()
				time.Sleep(300 * time.Millisecond)
				lanDNSBypass = false
			}

			hostOut, _ := exec.Command("dscacheutil", "-q", "host", "-a", "name", "google.com").Output()
			hostStr := string(hostOut)
			if strings.Contains(hostStr, "198.18.") {
				checks = append(checks, doctorCheck{
					Name:   "TUN DNS & Fake-IP",
					Status: "ok",
					Detail: "google.com resolves to Fake-IP (198.18.x.x) via utun hijack",
					Fixed:  doctorFix && lanDNSBypass,
				})
			} else if lanDNSBypass {
				checks = append(checks, doctorCheck{
					Name:   "TUN DNS & Fake-IP",
					Status: "warn",
					Detail: "macOS DNS points to LAN router (192.168.x.1 / fe80::1), bypassing utun",
					Hint:   "Run `manta doctor --fix` to switch DNS into utun Fake-IP hijack",
				})
			} else {
				checks = append(checks, doctorCheck{
					Name:   "TUN DNS & Fake-IP",
					Status: "warn",
					Detail: "google.com did not resolve to 198.18.x.x (may be using cached or direct DNS)",
					Hint:   "Run `manta conn close-all` or `manta doctor --fix`",
				})
			}
		}

		// 5. GeoIP / GeoSite Databases
		geoDB := filepath.Join(paths.RootDir, "geoip.metadb")
		countryDB := filepath.Join(paths.RootDir, "Country.mmdb")
		if st, err := os.Stat(geoDB); err == nil && st.Size() > 100*1024 {
			checks = append(checks, doctorCheck{
				Name:   "GeoIP Database",
				Status: "ok",
				Detail: fmt.Sprintf("geoip.metadb ready (%.1f MB)", float64(st.Size())/(1<<20)),
			})
		} else if st, err := os.Stat(countryDB); err == nil && st.Size() > 100*1024 {
			checks = append(checks, doctorCheck{
				Name:   "GeoIP Database",
				Status: "ok",
				Detail: fmt.Sprintf("Country.mmdb ready (%.1f MB)", float64(st.Size())/(1<<20)),
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:   "GeoIP Database",
				Status: "warn",
				Detail: "GeoIP database not found yet (domestic CN routing may fall through)",
				Hint:   "mihomo downloads geoip.metadb automatically on first startup",
			})
		}

		// 6. Subscription Profile & Quota
		if cfg.ActiveProfile == "" {
			checks = append(checks, doctorCheck{
				Name:   "Subscription Profile",
				Status: "warn",
				Detail: "no active subscription profile configured",
				Hint:   "Run `manta sub add main <subscription-url>`",
			})
		} else if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
			status := "ok"
			detail := fmt.Sprintf("active: %s", cfg.ActiveProfile)
			hint := ""
			if q := meta.QuotaSummary(); q != "" {
				detail += " (" + q + ")"
			}
			if meta.Total > 0 && (meta.Upload+meta.Download)*100/meta.Total >= 90 {
				status = "warn"
				hint = "Traffic quota is above 90%"
			}
			if meta.Expire > 0 && time.Now().Unix() > meta.Expire {
				status = "fail"
				hint = "Subscription has expired; renew or update profile"
			}
			checks = append(checks, doctorCheck{
				Name:   "Subscription Profile",
				Status: status,
				Detail: detail,
				Hint:   hint,
			})
		}

		// 7. Live Outbound Proxy Connectivity & Exit IP
		if running {
			proxyURL, _ := url.Parse("http://" + inboundAddr)
			hc := &http.Client{
				Timeout:   5 * time.Second,
				Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
			}
			start := time.Now()
			resp, err := hc.Get("https://www.gstatic.com/generate_204")
			rtt := time.Since(start).Milliseconds()
			if err != nil {
				checks = append(checks, doctorCheck{
					Name:   "Outbound Connectivity",
					Status: "fail",
					Detail: fmt.Sprintf("proxy request failed: %v", err),
					Hint:   "Run `manta node best` to switch to a healthy node",
				})
			} else {
				_ = resp.Body.Close()
				detail := fmt.Sprintf("HTTP %d via proxy in %dms", resp.StatusCode, rtt)
				if info, ipErr := api.FetchExitIP(cfg.MixedPort); ipErr == nil {
					detail += fmt.Sprintf(" • Exit: %s (%s)", info.IP, info.CountryCode)
				}
				checks = append(checks, doctorCheck{
					Name:   "Outbound Connectivity",
					Status: "ok",
					Detail: detail,
				})
			}
		}

		healthy := true
		for _, c := range checks {
			if c.Status == "fail" {
				healthy = false
			}
		}

		if doctorJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{
				"healthy": healthy,
				"checks":  checks,
			})
		}

		summaryBadge := cliOnline.Render("● HEALTHY")
		if !healthy {
			summaryBadge = cliOffline.Render("✗ ISSUES FOUND")
		}
		fmt.Printf("%s  %s  %s\n\n", cliBadge.Render("MANTA DOCTOR"), summaryBadge, cliMuted.Render(fmt.Sprintf("(%d checks)", len(checks))))

		for _, c := range checks {
			icon := cliOnline.Render("✓")
			if c.Status == "warn" {
				icon = cliWarn.Render("⚠")
			} else if c.Status == "fail" {
				icon = cliOffline.Render("✗")
			}
			fixedTag := ""
			if c.Fixed {
				fixedTag = " " + cliOnline.Render("[fixed]")
			}
			fmt.Printf("  %s  %-22s %s%s\n", icon, cliVal.Render(c.Name), c.Detail, fixedTag)
			if c.Hint != "" {
				fmt.Printf("     %s %s\n", cliMuted.Render("↳"), cliMuted.Render(c.Hint))
			}
		}
		return nil
	},
}
