package cli

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xfelixli/manta/internal/api"
)

func init() {
	rootCmd.AddCommand(diveCmd)
}

var oceanFortunes = []string{
	"Packets glide silently beneath the firewall — 0% turbulence in the Mariana Trench.",
	"A manta ray never stops swimming; neither does your TCP keep-alive pool.",
	"Deep currents run fastest where the routing table is cleanest.",
	"Smooth wings, zero jitter, sub-100ms across the Pacific.",
	"Encrypted payloads drifting effortlessly past the reef.",
}

var diveCmd = &cobra.Command{
	Use:    "dive",
	Hidden: true,
	Short:  "Enter the deep-sea manta telemetry card (easter egg)",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cfg, mgr, client, err := loadContext()
		if err != nil {
			return err
		}

		art := `
          ~   .     *        ~       .      ~
               __                 __
              /  \               /  \
         ~   / /\ \    .---.    / /\ \    ~
            / /  \ \__/ o o \__/ /  \ \
           /_/    \___   ▽   ___/    \_\
                .     \     /     .
           ~           \   /           ~
                        | |
                        | |     .
          .             ':'            ~
`
		fmt.Print(cliOnline.Render(art))

		running, _ := mgr.IsRunning()
		depth := fmt.Sprintf("-%dm (Surface)", cfg.MixedPort)
		beacon := "Offline"
		if running {
			depth = fmt.Sprintf("-%dm (Mariana Trench • TUN/Proxy)", cfg.MixedPort)
			if live, err := client.GetConfigs(); err == nil {
				if live.Tun.Enable {
					depth = fmt.Sprintf("-%dm (Abyssal Zone • utun %s)", cfg.MixedPort, emptyOr(live.Tun.Stack, "mixed"))
				}
			}
			if info, err := api.FetchExitIP(cfg.MixedPort); err == nil {
				loc := strings.TrimSpace(strings.Join([]string{info.City, info.CountryCode}, ", "))
				beacon = fmt.Sprintf("%s (%s)", info.IP, loc)
			} else {
				beacon = "127.0.0.1:" + fmt.Sprintf("%d", cfg.MixedPort)
			}
		}

		fmt.Printf("  %s  %s\n", cliBadge.Render("DEEP DIVE"), cliAccent.Render("Manta Ray Telemetry"))
		fmt.Println(renderKV("Depth", depth))
		fmt.Println(renderKV("Beacon", beacon))
		if meta, ok := cfg.Profiles[cfg.ActiveProfile]; ok {
			if q := meta.QuotaSummary(); q != "" {
				fmt.Println(renderKV("Oxygen", q))
			}
		}
		quote := oceanFortunes[rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(oceanFortunes))]
		fmt.Printf("\n  %s\n\n", cliMuted.Render("“"+quote+"”"))
		return nil
	},
}
