package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"cmfy/internal/comfy"
	"cmfy/internal/config"

	"github.com/spf13/cobra"
)

var (
	serverURL        string
	serverInspectRaw bool
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage server connection and capabilities",
}

var serverPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check connectivity to ComfyUI server",
	RunE:  serverPing,
}

var serverInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect deterministic ComfyUI server capabilities",
	RunE:  serverInspect,
}

var serverProfilesCmd = &cobra.Command{
	Use:   "profiles",
	Short: "List configured server profiles",
	RunE:  serverProfiles,
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.AddCommand(serverPingCmd, serverInspectCmd, serverProfilesCmd)
	serverPingCmd.Flags().StringVar(&serverURL, "url", "", "Override ComfyUI server URL")
	serverInspectCmd.Flags().StringVar(&serverURL, "url", "", "Override ComfyUI server URL")
	serverInspectCmd.Flags().BoolVar(&serverInspectRaw, "raw", false, "Include bounded raw object_info")
}

func serverPing(command *cobra.Command, _ []string) error {
	cfg, client, err := selectedClient()
	if err != nil {
		return err
	}
	if err := client.PingContext(command.Context()); err != nil {
		return err
	}
	result := map[string]any{"schema": "cmfy/server-ping-v1", "ok": true, "server_id": stableServerID(cfg.ServerURL)}
	if machineJSON {
		return emitJSON(result)
	}
	humanf("OK: %s\n", cfg.ServerURL)
	return nil
}

func serverInspect(command *cobra.Command, _ []string) error {
	cfg, client, err := selectedClient()
	if err != nil {
		return err
	}
	ctx := command.Context()
	objectInfo, err := client.ObjectInfoContext(ctx)
	if err != nil {
		return err
	}
	nodeTypes := make([]string, 0, len(objectInfo))
	for nodeType := range objectInfo {
		nodeTypes = append(nodeTypes, nodeType)
	}
	sort.Strings(nodeTypes)
	encoded, _ := emitCanonical(objectInfo)
	digest := sha256.Sum256(encoded)
	capabilities := map[string]bool{"history": true, "queue": true, "outputs": true, "models": false}
	modelInventory := map[string][]string{}
	warnings := make([]string, 0)
	folders, modelErr := client.ModelFoldersContext(ctx)
	if modelErr != nil {
		warnings = append(warnings, "model inventory unavailable: "+modelErr.Error())
	} else if len(folders) > 128 {
		warnings = append(warnings, "model folder count exceeds limit 128")
	} else {
		capabilities["models"] = true
		sort.Strings(folders)
		modelCount := 0
		modelBytes := 0
		for _, folder := range folders {
			models, err := client.ModelsContext(ctx, folder)
			if err != nil {
				warnings = append(warnings, "model folder "+folder+": "+err.Error())
				continue
			}
			sort.Strings(models)
			for _, model := range models {
				modelCount++
				modelBytes += len(model)
			}
			if modelCount > 100_000 || modelBytes > 8<<20 {
				warnings = append(warnings, "aggregate model inventory exceeds bounded output limit")
				capabilities["models"] = false
				modelInventory = map[string][]string{}
				break
			}
			modelInventory[folder] = models
		}
	}
	result := map[string]any{
		"schema":             "cmfy/server-inspection-v1",
		"server_id":          stableServerID(cfg.ServerURL),
		"node_count":         len(nodeTypes),
		"node_types":         nodeTypes,
		"object_info_digest": "sha256:" + hex.EncodeToString(digest[:]),
		"capabilities":       capabilities,
		"models":             modelInventory,
		"warnings":           warnings,
	}
	if serverInspectRaw {
		result["object_info"] = objectInfo
	}
	if machineJSON || serverInspectRaw {
		return emitJSON(result)
	}
	humanf("Server: %s\nNodes: %d\nObject info: %s\n", stableServerID(cfg.ServerURL), len(nodeTypes), result["object_info_digest"])
	return nil
}

func serverProfiles(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load()
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
	if machineJSON {
		return emitJSON(profiles)
	}
	for _, profile := range profiles {
		humanf("%s\t%s\n", profile["name"], profile["server_id"])
	}
	return nil
}

func selectedClient() (*config.Config, *comfy.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	if err := selectServer(cfg, serverURL); err != nil {
		return nil, nil, err
	}
	client, err := configuredClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	return cfg, client, nil
}

func stableServerID(url string) string {
	digest := sha256.Sum256([]byte(strings.TrimRight(strings.TrimSpace(url), "/")))
	return "server-" + hex.EncodeToString(digest[:8])
}

func emitCanonical(value any) ([]byte, error) {
	return json.Marshal(value)
}
