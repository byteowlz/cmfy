package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"

	"github.com/spf13/cobra"
)

var (
	queueJSON  bool
	jobJSON    bool
	jobTimeout time.Duration
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Show ComfyUI queue status",
	RunE:  queueStatus,
}

var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Manage prompt jobs",
}

var jobStatusCmd = &cobra.Command{
	Use:   "status <prompt_id>",
	Short: "Show status for a prompt ID",
	Args:  cobra.ExactArgs(1),
	RunE:  jobStatus,
}

var jobWaitCmd = &cobra.Command{
	Use:   "wait <prompt_id>",
	Short: "Wait until a prompt completes",
	Args:  cobra.ExactArgs(1),
	RunE:  jobWait,
}

var jobCancelCmd = &cobra.Command{
	Use:   "cancel <prompt_id>",
	Short: "Try to cancel/remove a prompt from queue",
	Args:  cobra.ExactArgs(1),
	RunE:  jobCancel,
}

func init() {
	rootCmd.AddCommand(queueCmd)
	rootCmd.AddCommand(jobCmd)

	jobCmd.AddCommand(jobStatusCmd)
	jobCmd.AddCommand(jobWaitCmd)
	jobCmd.AddCommand(jobCancelCmd)

	queueCmd.Flags().BoolVar(&queueJSON, "json", false, "Output as JSON")
	jobStatusCmd.Flags().BoolVar(&jobJSON, "json", false, "Output as JSON")
	jobWaitCmd.Flags().BoolVar(&jobJSON, "json", false, "Output as JSON")
	jobWaitCmd.Flags().DurationVar(&jobTimeout, "timeout", 30*time.Minute, "Maximum wait time")
}

type promptStatus struct {
	PromptID     string `json:"prompt_id"`
	Status       string `json:"status"`
	InQueue      bool   `json:"in_queue"`
	QueueSection string `json:"queue_section,omitempty"`
	HasHistory   bool   `json:"has_history"`
}

func queueStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c := comfy.NewClient(cfg.ServerURL)
	q, err := c.Queue()
	if err != nil {
		return err
	}

	running := queuePromptIDs(q["queue_running"])
	pending := queuePromptIDs(q["queue_pending"])

	if queueJSON {
		out := map[string]any{
			"running": running,
			"pending": pending,
			"raw":     q,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("Running: %d\n", len(running))
	for _, id := range running {
		fmt.Printf("  - %s\n", id)
	}
	fmt.Printf("Pending: %d\n", len(pending))
	for _, id := range pending {
		fmt.Printf("  - %s\n", id)
	}
	return nil
}

func jobStatus(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c := comfy.NewClient(cfg.ServerURL)
	status, err := getPromptStatus(c, args[0])
	if err != nil {
		return err
	}
	if jobJSON {
		b, _ := json.MarshalIndent(status, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("Prompt ID: %s\n", status.PromptID)
	fmt.Printf("Status: %s\n", status.Status)
	if status.InQueue {
		fmt.Printf("Queue: %s\n", status.QueueSection)
	}
	return nil
}

func jobWait(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c := comfy.NewClient(cfg.ServerURL)
	promptID := args[0]
	deadline := time.Now().Add(jobTimeout)

	last := ""
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for prompt %s", promptID)
		}
		status, err := getPromptStatus(c, promptID)
		if err != nil {
			return err
		}
		if status.Status != last {
			if !jobJSON {
				fmt.Println("status:", status.Status)
			}
			last = status.Status
		}

		if status.Status == "completed" || status.Status == "success" {
			if jobJSON {
				b, _ := json.MarshalIndent(status, "", "  ")
				fmt.Println(string(b))
			}
			return nil
		}
		if status.Status == "failed" || status.Status == "error" {
			return fmt.Errorf("prompt %s failed", promptID)
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

func jobCancel(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	c := comfy.NewClient(cfg.ServerURL)
	if err := c.DeleteFromQueue([]string{args[0]}); err != nil {
		return err
	}
	fmt.Println("Cancel request sent for prompt:", args[0])
	return nil
}

func getPromptStatus(c *comfy.Client, promptID string) (*promptStatus, error) {
	st := &promptStatus{PromptID: promptID, Status: "unknown"}

	hist, err := c.History(promptID)
	if err == nil {
		if entry, _ := hist[promptID].(map[string]any); entry != nil {
			st.HasHistory = true
			s := parseHistoryState(entry)
			if s != "" {
				st.Status = s
				if s == "completed" || s == "success" {
					return st, nil
				}
			}
		}
	}

	q, qErr := c.Queue()
	if qErr == nil {
		running := queuePromptIDs(q["queue_running"])
		pending := queuePromptIDs(q["queue_pending"])
		if contains(running, promptID) {
			st.InQueue = true
			st.QueueSection = "running"
			st.Status = "running"
			return st, nil
		}
		if contains(pending, promptID) {
			st.InQueue = true
			st.QueueSection = "pending"
			st.Status = "pending"
			return st, nil
		}
	}

	if st.HasHistory && (st.Status == "unknown" || st.Status == "") {
		st.Status = "finished"
	}
	if !st.HasHistory && !st.InQueue {
		st.Status = "not_found"
	}
	return st, nil
}

func parseHistoryState(entry map[string]any) string {
	state := ""
	if statusVal, ok := entry["status"]; ok {
		switch v := statusVal.(type) {
		case string:
			state = strings.ToLower(strings.TrimSpace(v))
		case map[string]any:
			if completed, ok := v["completed"].(bool); ok && completed {
				state = "completed"
			} else if statusStr, ok := v["status_str"].(string); ok {
				state = strings.ToLower(strings.TrimSpace(statusStr))
			}
		}
	}
	if state == "" {
		if outputs, ok := entry["outputs"].(map[string]any); ok && len(outputs) > 0 {
			state = "completed"
		}
	}
	return state
}

func queuePromptIDs(queueSection any) []string {
	var out []string
	switch arr := queueSection.(type) {
	case []any:
		for _, item := range arr {
			switch row := item.(type) {
			case []any:
				for _, col := range row {
					if s, ok := col.(string); ok && looksLikePromptID(s) {
						out = append(out, s)
						break
					}
				}
			case string:
				if looksLikePromptID(row) {
					out = append(out, row)
				}
			}
		}
	}
	return unique(out)
}

func looksLikePromptID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	return strings.Contains(s, "-") || len(s) >= 16
}

func unique(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}

func contains(items []string, target string) bool {
	for _, it := range items {
		if it == target {
			return true
		}
	}
	return false
}
