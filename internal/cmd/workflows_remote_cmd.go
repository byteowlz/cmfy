package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cmfy/internal/config"
	"cmfy/internal/remote"

	"github.com/spf13/cobra"
)

var workflowsSSHListJSON bool

var workflowsSSHListCmd = &cobra.Command{
	Use:   "ssh-list <server> [pattern]",
	Short: "List workflows from a remote server via SSH",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  workflowsSSHList,
}

var workflowsSSHImportCmd = &cobra.Command{
	Use:   "ssh-import <server> <remote-workflow> [local-name]",
	Short: "Import a workflow JSON from a remote server via SSH",
	Args:  cobra.RangeArgs(2, 3),
	RunE:  workflowsSSHImport,
}

func init() {
	workflowsCmd.AddCommand(workflowsSSHListCmd)
	workflowsCmd.AddCommand(workflowsSSHImportCmd)
	workflowsSSHListCmd.Flags().BoolVar(&workflowsSSHListJSON, "json", false, "Output as JSON")
}

func resolveRemoteServer(cfg *config.Config, name string) (config.RemoteServer, error) {
	srv, ok := cfg.RemoteServers[name]
	if !ok {
		return config.RemoteServer{}, fmt.Errorf("remote server %q not found in [remote_servers.%s]", name, name)
	}
	if strings.TrimSpace(srv.WorkflowsDir) == "" {
		return config.RemoteServer{}, fmt.Errorf("remote server %q is missing workflows_dir", name)
	}
	if strings.TrimSpace(srv.SSHConfigHost) == "" && strings.TrimSpace(srv.Host) == "" {
		return config.RemoteServer{}, fmt.Errorf("remote server %q needs either ssh_config_host or host", name)
	}
	return srv, nil
}

func workflowsSSHList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	srv, err := resolveRemoteServer(cfg, args[0])
	if err != nil {
		return err
	}

	pattern := ""
	if len(args) > 1 {
		pattern = args[1]
	}

	items, err := remote.ListWorkflowsViaSSH(srv, pattern)
	if err != nil {
		return err
	}

	if workflowsSSHListJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	if len(items) == 0 {
		fmt.Println("No remote workflows found")
		return nil
	}
	for _, item := range items {
		fmt.Println(item)
	}
	return nil
}

func workflowsSSHImport(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	srv, err := resolveRemoteServer(cfg, args[0])
	if err != nil {
		return err
	}

	remotePath := strings.TrimSpace(args[1])
	if remotePath == "" {
		return fmt.Errorf("remote workflow path is empty")
	}
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = strings.TrimRight(srv.WorkflowsDir, "/") + "/" + remotePath
	}

	localName := ""
	if len(args) == 3 {
		localName = strings.TrimSpace(args[2])
	} else {
		localName = filepath.Base(remotePath)
	}
	if !strings.HasSuffix(strings.ToLower(localName), ".json") {
		localName += ".json"
	}

	if err := os.MkdirAll(cfg.WorkflowsDir, 0o755); err != nil {
		return err
	}
	localPath := filepath.Join(cfg.WorkflowsDir, localName)
	if err := remote.CopyWorkflowViaSCP(srv, remotePath, localPath); err != nil {
		return err
	}

	fmt.Printf("Imported %s -> %s\n", remotePath, localPath)
	return nil
}
