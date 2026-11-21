package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

var connJSON bool

func init() {
	connListCmd.Flags().BoolVar(&connJSON, "json", false, "Output active connections as JSON")
	connCmd.AddCommand(connListCmd, connCloseAllCmd)
	rootCmd.AddCommand(connCmd)
}

var connCmd = &cobra.Command{
	Use:   "conn",
	Short: "Inspect or close active network connections",
}

var connListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List active connections sorted by traffic",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		summary, err := client.GetConnections()
		if err != nil {
			return err
		}
		if connJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(summary)
		}
		if len(summary.Connections) == 0 {
			fmt.Println("No active connections.")
			return nil
		}
		conns := summary.Connections
		sort.SliceStable(conns, func(i, j int) bool {
			return (conns[i].Download + conns[i].Upload) > (conns[j].Download + conns[j].Upload)
		})
		for _, c := range conns {
			fmt.Printf("  %-36s  %-14s  ↓%d B  ↑%d B\n", c.Target(), cliAccent.Render(c.Outbound()), c.Download, c.Upload)
		}
		return nil
	},
}

var connCloseAllCmd = &cobra.Command{
	Use:   "close-all",
	Short: "Close all active connections and flush Fake-IP DNS cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, _, client, err := loadContext()
		if err != nil {
			return err
		}
		if err := client.CloseAllConnections(); err != nil {
			return err
		}
		_ = client.FlushFakeIP()
		fmt.Printf("%s Closed all active connections & flushed Fake-IP cache\n", cliOnline.Render("✓"))
		return nil
	},
}
