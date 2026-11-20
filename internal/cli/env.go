package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var envUnset bool

func init() {
	envCmd.Flags().BoolVarP(&envUnset, "unset", "u", false, "Print unset statements to clear shell proxy variables")
	rootCmd.AddCommand(envCmd)
}

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Output shell export commands for HTTP/SOCKS5 proxy (use with eval)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if envUnset {
			fmt.Println("unset http_proxy https_proxy all_proxy HTTP_PROXY HTTPS_PROXY ALL_PROXY")
			return nil
		}
		_, cfg, _, _, err := loadContext()
		if err != nil {
			return err
		}
		httpURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.MixedPort)
		socksURL := fmt.Sprintf("socks5://127.0.0.1:%d", cfg.MixedPort)
		fmt.Printf("export http_proxy=%q\n", httpURL)
		fmt.Printf("export https_proxy=%q\n", httpURL)
		fmt.Printf("export all_proxy=%q\n", socksURL)
		fmt.Printf("export HTTP_PROXY=%q\n", httpURL)
		fmt.Printf("export HTTPS_PROXY=%q\n", httpURL)
		fmt.Printf("export ALL_PROXY=%q\n", socksURL)
		return nil
	},
}
