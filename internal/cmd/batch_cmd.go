package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

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
	batchJSON        bool
	batchStopOnError bool
	batchAsync       bool
	batchSubmitDelay time.Duration
	batchExampleMode string
)

var batchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Run multiple jobs from JSONL",
}

var batchRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Submit jobs from a JSONL file",
	RunE:  runBatch,
}

var batchRunExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Print JSONL batch examples to stdout",
	RunE:  runBatchExample,
}

func init() {
	rootCmd.AddCommand(batchCmd)
	batchCmd.AddCommand(batchRunCmd)
	batchRunCmd.AddCommand(batchRunExampleCmd)

	batchRunCmd.Flags().StringVarP(&batchFile, "file", "f", "", "Path to JSONL batch file")
	batchRunCmd.Flags().BoolVar(&batchJSON, "json", false, "Output machine-readable job results")
	batchRunCmd.Flags().BoolVar(&batchStopOnError, "stop-on-error", false, "Stop after first failed job")
	batchRunCmd.Flags().BoolVar(&batchAsync, "async", false, "Default to async submission for all jobs (line-level async still overrides)")
	batchRunCmd.Flags().DurationVar(&batchSubmitDelay, "submit-delay", 0, "Delay between submissions (throttling only; not execution concurrency)")

	batchRunExampleCmd.Flags().StringVar(&batchExampleMode, "mode", "mixed-workflows", "Example mode: minimal|full|mixed-workflows")
}

func runBatch(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(batchFile) == "" {
		return fmt.Errorf("--file is required")
	}
	f, err := os.Open(batchFile)
	if err != nil {
		return err
	}
	defer f.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	results := make([]batchResult, 0, 16)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var job batchJob
		if err := json.Unmarshal([]byte(line), &job); err != nil {
			res := batchResult{Line: lineNo, Status: "error", Error: fmt.Sprintf("invalid JSON: %v", err)}
			results = append(results, res)
			if batchStopOnError {
				break
			}
			continue
		}
		if strings.TrimSpace(job.Workflow) == "" {
			res := batchResult{Line: lineNo, ID: job.ID, Status: "error", Error: "workflow is required"}
			results = append(results, res)
			if batchStopOnError {
				break
			}
			continue
		}

		args := buildRunArgsFromJob(job)
		c := exec.Command(exe, args...)
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		c.Stdout = &stdout
		c.Stderr = &stderr

		if !batchJSON {
			fmt.Printf("[%d] submitting workflow=%s", lineNo, job.Workflow)
			if job.ID != "" {
				fmt.Printf(" id=%s", job.ID)
			}
			fmt.Println()
		}

		err := c.Run()
		out := stdout.String()
		errText := strings.TrimSpace(stderr.String())

		res := batchResult{Line: lineNo, ID: job.ID, Workflow: job.Workflow}
		if pid := extractPromptID(out); pid != "" {
			res.PromptID = pid
		}
		if err != nil {
			res.Status = "error"
			if errText == "" {
				errText = strings.TrimSpace(out)
			}
			if errText == "" {
				errText = err.Error()
			}
			res.Error = errText
			results = append(results, res)
			if !batchJSON {
				fmt.Printf("[%d] error: %s\n", lineNo, errText)
			}
			if batchStopOnError {
				break
			}
		} else {
			res.Status = "ok"
			results = append(results, res)
			if !batchJSON {
				if res.PromptID != "" {
					fmt.Printf("[%d] ok prompt_id=%s\n", lineNo, res.PromptID)
				} else {
					fmt.Printf("[%d] ok\n", lineNo)
				}
			}
		}

		if batchSubmitDelay > 0 {
			time.Sleep(batchSubmitDelay)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	if batchJSON {
		b, _ := json.MarshalIndent(results, "", "  ")
		fmt.Println(string(b))
		return nil
	}

	okCount := 0
	errCount := 0
	for _, r := range results {
		if r.Status == "ok" {
			okCount++
		} else {
			errCount++
		}
	}
	fmt.Printf("Batch done: %d ok, %d error\n", okCount, errCount)
	if errCount > 0 {
		return fmt.Errorf("batch completed with %d error(s)", errCount)
	}
	return nil
}

func buildRunArgsFromJob(job batchJob) []string {
	args := []string{"run", "-w", job.Workflow}

	if strings.TrimSpace(job.Server) != "" {
		args = append(args, "--server", job.Server)
	}

	effectiveAsync := batchAsync
	if job.Async != nil {
		effectiveAsync = *job.Async
	}
	if effectiveAsync {
		args = append(args, "--async")
	}

	if strings.TrimSpace(job.Timeout) != "" {
		args = append(args, "--timeout", strings.TrimSpace(job.Timeout))
	}

	if len(job.Vars) > 0 {
		keys := sortedKeys(job.Vars)
		for _, k := range keys {
			args = append(args, "--var", fmt.Sprintf("%s=%s", k, job.Vars[k]))
		}
	}

	if len(job.Set) > 0 {
		keys := sortedAnyKeys(job.Set)
		for _, k := range keys {
			args = append(args, "--set", fmt.Sprintf("%s=%s", k, anyToString(job.Set[k])))
		}
	}

	for _, v := range job.Image {
		args = append(args, "--image", v)
	}
	for _, v := range job.Mask {
		args = append(args, "--mask", v)
	}
	for _, v := range job.Input {
		args = append(args, "--input", v)
	}

	return args
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func extractPromptID(out string) string {
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "Prompt ID:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "Prompt ID:"))
		}
	}
	return ""
}

func runBatchExample(cmd *cobra.Command, args []string) error {
	switch strings.ToLower(strings.TrimSpace(batchExampleMode)) {
	case "minimal":
		fmt.Println(`{"workflow":"txt2img","vars":{"PROMPT":"sks man cinematic portrait","OUTPUT":"batch/sks_001"}}`)
	case "full":
		fmt.Println(`{"id":"job-001","workflow":"/Users/tommyfalkowski/cmfy/workflows/image_to_video_ltx2_3_i2v_with_sound.json","vars":{"PROMPT":"cinematic close-up, subtle camera motion","OUTPUT":"video/ltx23_batch_001"},"set":{"167:146.inputs.value":121},"image":["/Users/tommyfalkowski/cmfy/outputs/cmfy_movie_madness_00001_.png"],"server":"http://127.0.0.1:8188","async":true,"timeout":"30m"}`)
	case "mixed-workflows", "mixed":
		fmt.Println(`{"id":"img-1","workflow":"txt2img","vars":{"PROMPT":"sks man movie poster","OUTPUT":"batch/poster_001"},"set":{"12.inputs.steps":28},"async":true}`)
		fmt.Println(`{"id":"vid-1","workflow":"/Users/tommyfalkowski/cmfy/workflows/image_to_video_ltx2_3_i2v_with_sound.json","vars":{"PROMPT":"cinematic talking head shot","OUTPUT":"video/ltx23_vid_001"},"set":{"167:146.inputs.value":121},"image":["/Users/tommyfalkowski/cmfy/outputs/cmfy_movie_madness_00001_.png"],"async":true}`)
	default:
		return fmt.Errorf("invalid --mode %q, use minimal|full|mixed-workflows", batchExampleMode)
	}
	return nil
}
