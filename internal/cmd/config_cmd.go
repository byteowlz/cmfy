package cmd

import (
	"fmt"
	"os"
	"sort"

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

var configOutputCmd = &cobra.Command{
	Use:   "output",
	Short: "Show configured output directory path",
	RunE:  configOutput,
}

var configPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print configuration file content",
	RunE:  configPrint,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configOutputCmd)
	configCmd.AddCommand(configPrintCmd)
}

func configInit(cmd *cobra.Command, args []string) error {
	if err := icfg.InitDefault(); err != nil {
		return err
	}
	p, _ := icfg.Path()
	if machineJSON {
		return emitJSON(map[string]any{"schema": "cmfy/config-init-v1", "path": p})
	}
	humanf("Wrote default config: %s\n", p)
	return nil
}

func configPath(cmd *cobra.Command, args []string) error {
	p, err := icfg.Path()
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(map[string]any{"schema": "cmfy/config-path-v1", "path": p})
	}
	humanf("%s\n", p)
	return nil
}

func configOutput(cmd *cobra.Command, args []string) error {
	cfg, err := icfg.Load()
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(map[string]any{"schema": "cmfy/config-output-v1", "output_dir": cfg.OutputDir})
	}
	humanf("%s\n", cfg.OutputDir)
	return nil
}

func configPrint(cmd *cobra.Command, args []string) error {
	p, err := icfg.Path()
	if err != nil {
		return err
	}
	if machineJSON {
		cfg, err := icfg.Load()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Servers))
		for name := range cfg.Servers {
			names = append(names, name)
		}
		sort.Strings(names)
		profiles := make([]map[string]string, 0, len(names))
		for _, name := range names {
			profiles = append(profiles, map[string]string{"name": name, "server_id": stableServerID(cfg.Servers[name].URL)})
		}
		return emitJSON(map[string]any{
			"schema": "cmfy/config-v1", "path": p, "server_id": stableServerID(cfg.ServerURL),
			"output_dir": cfg.OutputDir, "workflows_dir": cfg.WorkflowsDir, "default_workflow": cfg.DefaultWorkflow,
			"limits":   map[string]any{"json_bytes": cfg.MaxJSONBytes, "upload_bytes": cfg.MaxUploadBytes, "output_bytes": cfg.MaxOutputBytes, "total_output_bytes": cfg.MaxTotalOutputBytes, "output_files": cfg.MaxOutputFiles},
			"profiles": profiles,
		})
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}
