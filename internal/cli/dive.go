package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
)

func init() {
	rootCmd.AddCommand(diveCmd)
}

var oceanQuotes = []string{
	"\"Packets glide silently beneath the firewall, where only the manta swims.\"",
	"\"In the Mariana Trench (-7,890m), every handshake is zero-loss.\"",
	"\"A calm surface hides a 10 Gbps deep-ocean current.\"",
	"\"No borders exist below the thermocline — only pure TCP/UDP flow.\"",
	"\"Sonar locked. Fake-IP beacons echoing at 0.2ms across the abyss.\"",
	"\"Ride the Kuroshio Current: low latency, wide wingspan, zero turbulence.\"",
}

func trenchName(cc, city string) string {
	switch strings.ToUpper(cc) {
	case "JP":
		return "Japan Trench • Kuroshio Current (" + emptyOr(city, "Tokyo") + ")"
	case "SG":
		return "Sunda Trench • Malacca Abyss (" + emptyOr(city, "Singapore") + ")"
	case "US":
		return "Monterey Canyon • Pacific Ridge (" + emptyOr(city, "San Jose") + ")"
	case "TW":
		return "Ryukyu Trench • Kuroshio Ridge (" + emptyOr(city, "Taipei") + ")"
	case "HK":
		return "South China Basin (" + emptyOr(city, "Hong Kong") + ")"
	default:
		if city != "" {
			return "Abyssal Plain (" + city + ")"
		}
		return "Open Ocean • International Waters"
	}
}

var diveCmd = &cobra.Command{
	Use:     "dive",
	Aliases: []string{"swim", "sonar"},
	Hidden:  true,
	Short:   "Easter egg: Deep-sea sonar telemetry & Manta Ray glider",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}

		art := []string{
			"               ·  ˚  .   🫧   .   ˚  ·",
			"                   ╭◜◝ ͡ ◜◝╮    ╭◜◝ ͡ ◜◝╮",
			"                ╱￣￣╲  ╰◟◞ ͜ ◟◞╯  ╱￣￣╲",
			"             ╱￣      ╲___╭──╮___╱      ￣╲",
			"          ╭─╯   ●         │  │         ●   ╰─╮",
			"           ╲___          ╭╯  ╰╮          ___╱",
			"               ￣￣╲____╱      ╲____╱￣￣",
			"                       │   │   │",
			"                        ╲  │  ╱",
			"                         ╲ │ ╱",
			"                          ╲│╱",
			"                           ▼",
		}

		fmt.Println()
		for _, line := range art {
			fmt.Println("  " + cliAccent.Render(line))
		}
		fmt.Println()

		running, pid := mgr.IsRunning()
		depth := fmt.Sprintf("-%dm (Thermocline • Standby)", cfg.MixedPort)
		sonarState := cliOffline.Render("○ SURFACE DOCKED")
		activeConns := 0
		sessionBytes := int64(0)

		if running {
			sonarState = cliOnline.Render("● SUBMERGED") + cliMuted.Render(fmt.Sprintf(" (vessel pid %d)", pid))
			if live, err := client.GetConfigs(); err == nil {
				if live.Tun.Enable {
					depth = fmt.Sprintf("-%dm (Mariana Trench • utun %s)", cfg.MixedPort, emptyOr(live.Tun.Stack, "Mixed"))
				} else {
					depth = fmt.Sprintf("-%dm (Continental Shelf • %s)", cfg.MixedPort, strings.ToUpper(live.Mode))
				}
			}
			if summary, err := client.GetConnections(); err == nil {
				activeConns = len(summary.Connections)
				sessionBytes = summary.DownloadTotal + summary.UploadTotal
			}
		}

		beacon := "Scanning abyssal beacons..."
		if running {
			if info, err := api.FetchExitIP(cfg.MixedPort); err == nil {
				beacon = fmt.Sprintf("%s  •  %s", cliVal.Render(info.IP), trenchName(info.CountryCode, info.City))
			} else {
				beacon = cliMuted.Render("Silent Running (stealth beacon)")
			}
		}

		fmt.Printf("  %s  %s\n", cliBadge.Render("MANTA SONAR"), sonarState)
		fmt.Println(renderKV("Depth", cliVal.Render(depth)))
		fmt.Println(renderKV("Beacon", beacon))
		fmt.Println(renderKV("Current", fmt.Sprintf("%d active streams  •  %s displaced", activeConns, formatDiveBytes(sessionBytes))))
		if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
			if q := meta.QuotaSummary(); q != "" {
				fmt.Println(renderKV("Reserves", q))
			}
		}

		quote := oceanQuotes[time.Now().UnixNano()%int64(len(oceanQuotes))]
		fmt.Println()
		fmt.Println("  " + cliMuted.Render(quote))
		fmt.Println("  " + cliMuted.Render("Tip: press `~` (or click the MANTA badge) inside `manta tui` for live Sonar Mode."))
		fmt.Println()
		return nil
	},
}

func formatDiveBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
