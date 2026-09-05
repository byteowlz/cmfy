package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"cmfy/internal/jobs"

	"github.com/spf13/cobra"
)

type batchJob struct {
	ID       string            `json:"id,omitempty"`
	Workflow string            `json:"workflow"`
	Vars     map[string]string `json:"vars,omitempty"`
	Set      map[string]any    `json:"set,omitempty"`
	Image    []string          `json:"image,omitempty"`
	Mask     []string          `json:"mask,omitempty"`
	Input    []string          `json:"input,omitempty"`
	Server   string            `json:"server,omitempty"`
	Async    *bool             `json:"async,omitempty"`
	Timeout  string            `json:"timeout,omitempty"`
}

type batchItem struct {
	line int
	job  batchJob
}

type batchResult struct {
	Line     int    `json:"line"`
	ID       string `json:"id,omitempty"`
	Workflow string `json:"workflow"`
	PromptID string `json:"prompt_id,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

var (
	batchFile        string
	batchStopOnError bool
	batchAsync       bool
	batchSubmitDelay time.Duration
	batchConcurrency int
	batchExampleMode string
)

var batchCmd = &cobra.Command{Use: "batch", Short: "Run multiple jobs from JSONL"}
var batchRunCmd = &cobra.Command{Use: "run", Short: "Submit jobs from a JSONL file", RunE: runBatch}
var batchRunExampleCmd = &cobra.Command{Use: "example", Short: "Print JSONL batch examples to stdout", RunE: runBatchExample}

func init() {
	rootCmd.AddCommand(batchCmd)
	batchCmd.AddCommand(batchRunCmd)
	batchRunCmd.AddCommand(batchRunExampleCmd)
	batchRunCmd.Flags().StringVarP(&batchFile, "file", "f", "", "Path to JSONL batch file")
	batchRunCmd.Flags().BoolVar(&batchStopOnError, "stop-on-error", false, "Stop scheduling after the first failed job")
	batchRunCmd.Flags().BoolVar(&batchAsync, "async", false, "Default to async submission for all jobs")
	batchRunCmd.Flags().DurationVar(&batchSubmitDelay, "submit-delay", 0, "Minimum delay between submission starts")
	batchRunCmd.Flags().IntVar(&batchConcurrency, "concurrency", 1, "Maximum concurrent jobs (1-32)")
	batchRunExampleCmd.Flags().StringVar(&batchExampleMode, "mode", "mixed-workflows", "Example mode: minimal|full|mixed-workflows")
}

func runBatch(command *cobra.Command, _ []string) error {
	if strings.TrimSpace(batchFile) == "" {
		return errorsNew("--file is required")
	}
	items, invalid, err := readBatch(batchFile)
	if err != nil {
		return err
	}
	if batchConcurrency < 1 || batchConcurrency > 32 {
		return fmt.Errorf("--concurrency must be between 1 and 32")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(command.Context())
	defer cancel()
	work := make(chan batchItem)
	results := make(chan batchResult, len(items))
	var workers sync.WaitGroup
	for range batchConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for item := range work {
				result := executeBatchItem(ctx, executable, item)
				results <- result
				if batchStopOnError && result.Status == "error" {
					cancel()
				}
			}
		}()
	}
	go func() {
		defer close(work)
		for index, item := range items {
			select {
			case <-ctx.Done():
				return
			case work <- item:
			}
			if batchSubmitDelay > 0 && index < len(items)-1 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(batchSubmitDelay):
				}
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	allResults := append([]batchResult(nil), invalid...)
	for result := range results {
		allResults = append(allResults, result)
	}
	sort.Slice(allResults, func(i, j int) bool { return allResults[i].Line < allResults[j].Line })
	if machineJSON {
		if err := emitJSON(allResults); err != nil {
			return err
		}
	} else {
		for _, result := range allResults {
			if result.Status == "error" {
				humanf("[%d] error: %s\n", result.Line, result.Error)
			} else {
				humanf("[%d] %s prompt_id=%s\n", result.Line, result.Status, result.PromptID)
			}
		}
	}
	errorCount := 0
	for _, result := range allResults {
		if result.Status == "error" {
			errorCount++
		}
	}
	if !machineJSON {
		humanf("Batch done: %d ok, %d error\n", len(allResults)-errorCount, errorCount)
	}
	if errorCount > 0 {
		err := fmt.Errorf("batch completed with %d error(s)", errorCount)
		if machineJSON {
			return Reported(err)
		}
		return err
	}
	return nil
}

func readBatch(path string) ([]batchItem, []batchResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 10<<20)
	items := make([]batchItem, 0, 16)
	invalid := make([]batchResult, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var job batchJob
		if err := json.Unmarshal([]byte(line), &job); err != nil {
			invalid = append(invalid, batchResult{Line: lineNumber, Status: "error", Error: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		if strings.TrimSpace(job.Workflow) == "" {
			invalid = append(invalid, batchResult{Line: lineNumber, ID: job.ID, Status: "error", Error: "workflow is required"})
			continue
		}
		items = append(items, batchItem{line: lineNumber, job: job})
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return items, invalid, nil
}

func executeBatchItem(ctx context.Context, executable string, item batchItem) batchResult {
	arguments := buildRunArgsFromJob(item.job)
	command := exec.CommandContext(ctx, executable, arguments...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := batchResult{Line: item.line, ID: item.job.ID, Workflow: item.job.Workflow}
	if err != nil {
		result.Status = "error"
		result.Error = boundedText(stderr.String(), stdout.String(), err.Error())
		return result
	}
	var job jobs.Record
	if err := json.Unmarshal(stdout.Bytes(), &job); err != nil {
		result.Status = "error"
		result.Error = "child cmfy returned invalid JSON: " + err.Error()
		return result
	}
	result.PromptID = job.PromptID
	result.Status = job.Status
	if result.Status == "" {
		result.Status = "ok"
	}
	return result
}

func buildRunArgsFromJob(job batchJob) []string {
	arguments := []string{"--json"}
	if cfgFile != "" {
		arguments = append(arguments, "--config", cfgFile)
	}
	if stateDir != "" {
		arguments = append(arguments, "--state-dir", stateDir)
	}
	if serverProfile != "" {
		arguments = append(arguments, "--profile", serverProfile)
	}
	arguments = append(arguments, "run", "-w", job.Workflow)
	if job.ID != "" {
		arguments = append(arguments, "--request-id", job.ID)
	}
	if strings.TrimSpace(job.Server) != "" {
		arguments = append(arguments, "--server", job.Server)
	}
	effectiveAsync := batchAsync
	if job.Async != nil {
		effectiveAsync = *job.Async
	}
	if effectiveAsync {
		arguments = append(arguments, "--async")
	}
	if strings.TrimSpace(job.Timeout) != "" {
		arguments = append(arguments, "--timeout", strings.TrimSpace(job.Timeout))
	}
	for _, key := range sortedKeys(job.Vars) {
		arguments = append(arguments, "--var", fmt.Sprintf("%s=%s", key, job.Vars[key]))
	}
	for _, key := range sortedAnyKeys(job.Set) {
		arguments = append(arguments, "--set", fmt.Sprintf("%s=%s", key, anyToString(job.Set[key])))
	}
	for _, value := range job.Image {
		arguments = append(arguments, "--image", value)
	}
	for _, value := range job.Mask {
		arguments = append(arguments, "--mask", value)
	}
	for _, value := range job.Input {
		arguments = append(arguments, "--input", value)
	}
	return arguments
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func anyToString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func boundedText(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if len(candidate) > 4096 {
			return candidate[:4096]
		}
		return candidate
	}
	return "batch job failed"
}

func errorsNew(message string) error { return fmt.Errorf("%s", message) }

func runBatchExample(_ *cobra.Command, _ []string) error {
	switch strings.ToLower(strings.TrimSpace(batchExampleMode)) {
	case "minimal":
		fmt.Println(`{"workflow":"txt2img","vars":{"PROMPT":"sks man cinematic portrait","OUTPUT":"batch/sks_001"}}`)
	case "full":
		fmt.Println(`{"id":"job-001","workflow":"image_to_video_ltx2_3_i2v_with_sound","vars":{"PROMPT":"cinematic close-up","OUTPUT":"video/ltx23_batch_001"},"image":["input.png"],"async":true,"timeout":"30m"}`)
	case "mixed-workflows", "mixed":
		fmt.Println(`{"id":"img-1","workflow":"txt2img","vars":{"PROMPT":"sks man movie poster","OUTPUT":"batch/poster_001"},"async":true}`)
		fmt.Println(`{"id":"vid-1","workflow":"img2vid","vars":{"PROMPT":"cinematic talking head shot","OUTPUT":"video/ltx23_vid_001"},"image":["input.png"],"async":true}`)
	default:
		return fmt.Errorf("invalid --mode %q, use minimal|full|mixed-workflows", batchExampleMode)
	}
	return nil
}
