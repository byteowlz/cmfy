package remote

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"cmfy/internal/config"
)

func buildSSHBaseArgs(s config.RemoteServer) []string {
	args := make([]string, 0, 8)
	if s.Port > 0 {
		args = append(args, "-p", strconv.Itoa(s.Port))
	}
	if strings.TrimSpace(s.KeyPath) != "" {
		args = append(args, "-i", s.KeyPath)
	}
	if strings.TrimSpace(s.SSHConfigHost) != "" {
		args = append(args, s.SSHConfigHost)
		return args
	}
	target := strings.TrimSpace(s.Host)
	if strings.TrimSpace(s.User) != "" {
		target = fmt.Sprintf("%s@%s", s.User, s.Host)
	}
	args = append(args, target)
	return args
}

func quoteSingle(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func ListWorkflowsViaSSH(s config.RemoteServer, pattern string) ([]string, error) {
	if strings.TrimSpace(s.WorkflowsDir) == "" {
		return nil, fmt.Errorf("remote server is missing workflows_dir")
	}

	glob := "*.json"
	if strings.TrimSpace(pattern) != "" {
		glob = "*" + pattern + "*.json"
	}

	remoteCmd := fmt.Sprintf(
		"set -eu; if [ -d %s ]; then find %s -type f -iname %s | sort; fi",
		quoteSingle(s.WorkflowsDir),
		quoteSingle(s.WorkflowsDir),
		quoteSingle(glob),
	)

	args := buildSSHBaseArgs(s)
	args = append(args, remoteCmd)
	cmd := exec.Command("ssh", args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh list failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	out := make([]string, 0, len(lines))
	prefix := strings.TrimRight(filepath.Clean(s.WorkflowsDir), "/") + "/"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimPrefix(line, prefix)
		}
		out = append(out, line)
	}
	return out, nil
}

func CopyWorkflowViaSCP(s config.RemoteServer, remoteWorkflowPath, localPath string) error {
	if strings.TrimSpace(remoteWorkflowPath) == "" {
		return fmt.Errorf("remote workflow path is empty")
	}

	args := make([]string, 0, 8)
	if s.Port > 0 {
		args = append(args, "-P", strconv.Itoa(s.Port))
	}
	if strings.TrimSpace(s.KeyPath) != "" {
		args = append(args, "-i", s.KeyPath)
	}

	targetHost := strings.TrimSpace(s.Host)
	if strings.TrimSpace(s.SSHConfigHost) != "" {
		targetHost = s.SSHConfigHost
	}
	if strings.TrimSpace(s.User) != "" && strings.TrimSpace(s.SSHConfigHost) == "" {
		targetHost = fmt.Sprintf("%s@%s", s.User, s.Host)
	}

	args = append(args, fmt.Sprintf("%s:%s", targetHost, remoteWorkflowPath), localPath)
	cmd := exec.Command("scp", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scp import failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
