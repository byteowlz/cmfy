package cmd

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"cmfy/internal/config"
	"cmfy/internal/engine"
	"cmfy/internal/jobs"

	"github.com/spf13/cobra"
)

var (
	jobsLimit           int
	jobsCursor          string
	jobsStatus          string
	jobsServer          string
	watchJSONL          bool
	watchIncludePreview bool
	watchInterval       time.Duration
	retryRequestID      string
	pruneOlderThan      time.Duration
	pruneUploadsAge     time.Duration
	pruneKeepRecent     int
	pruneDryRun         bool
)

var jobsCmd = &cobra.Command{
	Use:   "jobs",
	Short: "List, inspect, watch, and retry durable jobs",
}

var jobsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List durable jobs newest first",
	RunE:  listJobs,
}

var jobsShowCmd = &cobra.Command{
	Use:   "show <job_or_prompt_id>",
	Short: "Show one durable job without network reconciliation",
	Args:  cobra.ExactArgs(1),
	RunE:  showJob,
}

var jobsStatusCmd = &cobra.Command{
	Use:   "status <job_or_prompt_id>",
	Short: "Reconcile one durable job with ComfyUI",
	Args:  cobra.ExactArgs(1),
	RunE:  statusJob,
}

var jobsWatchCmd = &cobra.Command{
	Use:   "watch <job_or_prompt_id>",
	Short: "Stream job state changes",
	Args:  cobra.ExactArgs(1),
	RunE:  watchJob,
}

var jobsRetryCmd = &cobra.Command{
	Use:   "retry <job_or_prompt_id>",
	Short: "Submit a new job from a durable request",
	Args:  cobra.ExactArgs(1),
	RunE:  retryJob,
}

var jobsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Prune old terminal history and stale upload-cache entries",
	RunE:  pruneJobs,
}

func init() {
	rootCmd.AddCommand(jobsCmd)
	jobsCmd.AddCommand(jobsListCmd, jobsShowCmd, jobsStatusCmd, jobsWatchCmd, jobsRetryCmd, jobsPruneCmd)
	jobsListCmd.Flags().IntVar(&jobsLimit, "limit", 50, "Maximum jobs to return (1-200)")
	jobsListCmd.Flags().StringVar(&jobsCursor, "cursor", "", "Opaque continuation cursor")
	jobsListCmd.Flags().StringVar(&jobsStatus, "status", "", "Filter by exact status")
	jobsListCmd.Flags().StringVar(&jobsServer, "server-id", "", "Filter by stable server identity")
	jobsWatchCmd.Flags().BoolVar(&watchJSONL, "jsonl", false, "Emit one JSON event per line")
	jobsWatchCmd.Flags().BoolVar(&watchIncludePreview, "include-preview", false, "Include bounded preview media as base64 in JSONL events")
	jobsWatchCmd.Flags().DurationVar(&watchInterval, "interval", 1500*time.Millisecond, "Polling fallback interval")
	jobsRetryCmd.Flags().StringVar(&retryRequestID, "request-id", "", "Idempotency key for the retried submission")
	jobsPruneCmd.Flags().DurationVar(&pruneOlderThan, "older-than", 30*24*time.Hour, "Prune terminal jobs older than this age")
	jobsPruneCmd.Flags().DurationVar(&pruneUploadsAge, "uploads-older-than", 7*24*time.Hour, "Prune upload-cache entries older than this age")
	jobsPruneCmd.Flags().IntVar(&pruneKeepRecent, "keep-recent", 1000, "Always retain this many newest jobs")
	jobsPruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "Report eligible records without deleting them")
}

func listJobs(_ *cobra.Command, _ []string) error {
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	page, err := store.List(context.Background(), jobs.ListOptions{Limit: jobsLimit, Cursor: jobsCursor, Status: jobsStatus, ServerID: jobsServer})
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(page)
	}
	for _, job := range page.Jobs {
		humanf("%s\t%s\t%s\t%s\n", job.PromptID, job.Status, job.Workflow, job.Prompt)
	}
	if page.NextCursor != "" {
		humanf("Next cursor: %s\n", page.NextCursor)
	}
	return nil
}

func showJob(_ *cobra.Command, args []string) error {
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	job, err := store.Get(context.Background(), args[0])
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(job)
	}
	humanf("Prompt ID: %s\nStatus: %s\nWorkflow: %s\nPrompt: %s\nSubmitted: %s\nOutputs: %d\n", job.PromptID, job.Status, job.Workflow, job.Prompt, job.SubmittedAt.Format(time.RFC3339), len(job.Outputs))
	return nil
}

func statusJob(command *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	client, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	service := engine.New(engine.Options{Config: cfg, Client: client, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
	job, err := service.Observe(command.Context(), args[0])
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(job)
	}
	humanf("Prompt ID: %s\nStatus: %s\n", job.PromptID, job.Status)
	return nil
}

func watchJob(command *cobra.Command, args []string) error {
	if machineJSON {
		return errors.New("jobs watch is streaming; use --jsonl instead of global --json")
	}
	if watchIncludePreview && !watchJSONL {
		return errors.New("--include-preview requires --jsonl")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	client, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	service := engine.New(engine.Options{Config: cfg, Client: client, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
	events, failures := service.Watch(command.Context(), args[0], watchInterval)
	for event := range events {
		if watchJSONL {
			if watchIncludePreview && len(event.Preview) > 0 {
				event.PreviewBase64 = base64.StdEncoding.EncodeToString(event.Preview)
			}
			if err := emitJSON(event); err != nil {
				return err
			}
		} else if event.Type == "progress" && event.Max > 0 {
			humanf("%s\t%s\t%d/%d\n", event.Time.Format(time.RFC3339), event.Type, event.Value, event.Max)
		} else {
			humanf("%s\t%s\n", event.Time.Format(time.RFC3339), event.Type)
		}
	}
	return <-failures
}

func retryJob(command *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := selectServer(cfg, ""); err != nil {
		return err
	}
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	original, err := store.Get(command.Context(), args[0])
	if err != nil {
		return err
	}
	request, err := requestFromRecord(original)
	if err != nil {
		return err
	}
	request.RequestID = retryRequestID
	client, err := configuredClient(cfg)
	if err != nil {
		return err
	}
	service := engine.New(engine.Options{Config: cfg, Client: client, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
	plan, err := service.Resolve(command.Context(), request)
	if err != nil {
		return err
	}
	job, _, err := service.Submit(command.Context(), plan)
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(job)
	}
	humanf("Prompt ID: %s\n", job.PromptID)
	return nil
}

func pruneJobs(command *cobra.Command, _ []string) error {
	if pruneOlderThan <= 0 || pruneUploadsAge <= 0 || pruneKeepRecent < 0 {
		return errors.New("retention ages must be positive and --keep-recent cannot be negative")
	}
	store, err := openJobStore()
	if err != nil {
		return err
	}
	defer store.Close()
	now := time.Now().UTC()
	jobsBefore := now.Add(-pruneOlderThan)
	uploadsBefore := now.Add(-pruneUploadsAge)
	jobCount, err := store.CountPrunable(command.Context(), jobsBefore, pruneKeepRecent)
	if err != nil {
		return err
	}
	uploadCount, err := store.CountPrunableUploads(command.Context(), uploadsBefore)
	if err != nil {
		return err
	}
	if !pruneDryRun {
		jobCount, err = store.Prune(command.Context(), jobsBefore, pruneKeepRecent)
		if err != nil {
			return err
		}
		uploadCount, err = store.PruneUploads(command.Context(), uploadsBefore)
		if err != nil {
			return err
		}
	}
	result := map[string]any{"schema": "cmfy/jobs-prune-v1", "dry_run": pruneDryRun, "jobs": jobCount, "uploads": uploadCount}
	if machineJSON {
		return emitJSON(result)
	}
	humanf("Jobs: %d\nUpload cache: %d\n", jobCount, uploadCount)
	return nil
}

func requestFromRecord(record jobs.Record) (engine.Request, error) {
	request := engine.Request{Workflow: record.Workflow, Prompt: record.Prompt, Parameters: map[string]any{}}
	for key, value := range record.Parameters {
		switch key {
		case "variables":
			raw, ok := value.(map[string]any)
			if !ok {
				return engine.Request{}, errors.New("stored variables are malformed")
			}
			request.Variables = make(map[string]string, len(raw))
			for variable, rawValue := range raw {
				text, ok := rawValue.(string)
				if !ok {
					return engine.Request{}, fmt.Errorf("stored variable %s is not a string", variable)
				}
				request.Variables[variable] = text
			}
		case "sets":
			raw, ok := value.([]any)
			if !ok {
				return engine.Request{}, errors.New("stored sets are malformed")
			}
			for _, item := range raw {
				text, ok := item.(string)
				if !ok {
					return engine.Request{}, errors.New("stored set is not a string")
				}
				request.Sets = append(request.Sets, text)
			}
		case "output_dir":
			request.OutputDir, _ = value.(string)
		default:
			request.Parameters[key] = value
		}
	}
	for _, input := range record.Inputs {
		switch input.Kind {
		case "image":
			request.Images = append(request.Images, input.Path)
		case "mask":
			request.Masks = append(request.Masks, input.Path)
		case "input":
			request.Inputs = append(request.Inputs, input.Path)
		}
	}
	return request, nil
}
