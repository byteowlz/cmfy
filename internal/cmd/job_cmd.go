package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/engine"
	"cmfy/internal/jobs"
	"cmfy/internal/output"

	"github.com/spf13/cobra"
)

var (
	jobTimeout  time.Duration
	downloadOut bool
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

	jobWaitCmd.Flags().DurationVar(&jobTimeout, "timeout", 30*time.Minute, "Maximum wait time")
	jobWaitCmd.Flags().BoolVar(&downloadOut, "download", false, "Download outputs after completion")
	jobWaitCmd.Flags().StringVarP(&outDir, "output", "o", "", "Output directory override")
	jobWaitCmd.Flags().StringVar(&outDir, "output-dir", "", "Output directory override (alias for --output)")
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
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	c, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	q, err := c.QueueContext(cmd.Context())
	if err != nil {
		return err
	}

	running := queuePromptIDs(q["queue_running"])
	pending := queuePromptIDs(q["queue_pending"])

	if machineJSON {
		return emitJSON(map[string]any{"schema": "cmfy/queue-v1", "running": running, "pending": pending})
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
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	c, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	status, err := getPromptStatus(c, args[0])
	if err != nil {
		return err
	}
	if err := updateDurableStatus(cmd.Context(), args[0], status.Status); err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(status)
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
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	if outDir != "" {
		cfg.OutputDir = outDir
	}
	c, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	promptID := args[0]
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if _, getErr := store.Get(cmd.Context(), promptID); getErr == nil {
		service := engine.New(engine.Options{Config: cfg, Client: c, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
		ctx, cancel := context.WithTimeout(cmd.Context(), jobTimeout)
		defer cancel()
		record, waitErr := service.Wait(ctx, promptID, 1500*time.Millisecond)
		if waitErr != nil {
			return waitErr
		}
		if downloadOut {
			record, waitErr = service.Collect(ctx, promptID)
			if waitErr != nil {
				return waitErr
			}
		}
		if machineJSON {
			return emitJSON(record)
		}
		humanf("Prompt ID: %s\nStatus: %s\n", record.PromptID, record.Status)
		return nil
	} else if !errors.Is(getErr, jobs.ErrNotFound) {
		return getErr
	}
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
			if !machineJSON && !quiet {
				fmt.Println("status:", status.Status)
			}
			last = status.Status
		}

		if status.Status == "completed" || status.Status == "success" {
			// Download outputs if requested
			if downloadOut {
				hist, err := c.History(promptID)
				if err == nil {
					entry, ok := hist[promptID].(map[string]any)
					if ok {
						outputs := getMap(entry, "outputs")
						if len(outputs) > 0 {
							if err := downloadOutputsFromMap(c, outputs, cfg); err != nil {
								return fmt.Errorf("download outputs: %w", err)
							}
						}
					}
				}
			}
			if machineJSON {
				return emitJSON(status)
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
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	client, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.Get(cmd.Context(), args[0]); err == nil {
		service := engine.New(engine.Options{Config: cfg, Client: client, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
		result, err := service.Cancel(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if machineJSON {
			return emitJSON(result)
		}
		humanf("Cancellation %s for prompt %s (status: %s)\n", result.Outcome, result.PromptID, result.Status)
		return nil
	} else if !errors.Is(err, jobs.ErrNotFound) {
		return err
	}
	status, err := getPromptStatus(client, args[0])
	if err != nil {
		return err
	}
	outcome := "request_sent"
	resultStatus := "cancelling"
	switch status.Status {
	case "running":
		err = client.InterruptContext(cmd.Context())
	case "pending", "queued":
		err = client.DeleteFromQueueContext(cmd.Context(), []string{args[0]})
	case "completed", "success", "failed", "error", "cancelled":
		outcome = "already_terminal"
		resultStatus = status.Status
	default:
		outcome = "not_found"
		resultStatus = status.Status
	}
	if err != nil {
		return err
	}
	result := map[string]any{"schema": "cmfy/cancellation-v1", "prompt_id": args[0], "previous_status": status.Status, "status": resultStatus, "outcome": outcome}
	if machineJSON {
		return emitJSON(result)
	}
	humanf("Cancellation %s for prompt %s (status: %s)\n", outcome, args[0], resultStatus)
	return nil
}

func updateDurableStatus(ctx context.Context, promptID, status string) error {
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if _, err := store.Get(ctx, promptID); err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return nil
		}
		return err
	}
	return store.Update(ctx, promptID, jobs.Update{Status: status})
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

// downloadOutputsFromMap fetches and saves outputs from a completed prompt.
// ComfyUI returns outputs under various keys depending on node type:
// "images" (SaveImage), "gifs"/"animated" (AnimateDiff/SaveAnimatedWEBP),
// "videos" (SaveVideo), "audio" (SaveAudioMP3/SaveAudio). Check all keys that contain file-reference arrays.
func downloadOutputsFromMap(c *comfy.Client, outputs map[string]any, cfg *config.Config) error {
	descriptors, err := output.Descriptors(outputs)
	if err != nil {
		return err
	}
	assets, err := output.Collect(context.Background(), c, cfg.OutputDir, descriptors, output.Limits{})
	if err != nil {
		return err
	}
	for _, asset := range assets {
		humanf("Saved: %s\n", asset.Path)
	}
	humanf("Workflow completed (%d file(s) saved)\n", len(assets))
	return nil
}
