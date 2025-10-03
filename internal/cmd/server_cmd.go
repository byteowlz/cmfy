package cmd

import (
	"fmt"
	"strings"

	"cmfy/internal/comfy"
	"cmfy/internal/config"

	"github.com/spf13/cobra"
)

var serverURL string

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage server connection",
}

var serverPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check connectivity to ComfyUI server",
	RunE:  serverPing,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverPingCmd)
	serverPingCmd.Flags().StringVar(&serverURL, "url", "", "Override ComfyUI server URL")
}

func serverPing(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if serverURL != "" {
		cfg.ServerURL = strings.TrimSpace(serverURL)
	}
	c := comfy.NewClient(cfg.ServerURL)
	if err := c.Ping(); err != nil {
		return err
	}
	fmt.Println("OK:", cfg.ServerURL)
	return nil
}
