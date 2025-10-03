package cmd

import (
	"fmt"

	icfg "cmfy/internal/config"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize default configuration",
	RunE:  configInit,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show configuration file path",
	RunE:  configPath,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configPathCmd)
}

func configInit(cmd *cobra.Command, args []string) error {
	if err := icfg.InitDefault(); err != nil {
		return err
	}
	p, _ := icfg.Path()
	fmt.Println("Wrote default config:", p)
	return nil
}

func configPath(cmd *cobra.Command, args []string) error {
	p, err := icfg.Path()
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}
