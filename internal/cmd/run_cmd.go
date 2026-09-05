package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cmfy/internal/config"
	"cmfy/internal/engine"
	"cmfy/internal/jobs"

	"github.com/spf13/cobra"
)

var (
	workflowName string
	baseURL      string
	outDir       string
	outputName   string
	promptText   string
	tagsText     string
	lyricsText   string
	maxTokens    int
	seed         int
	width        int
	height       int
	steps        int
	cfgScale     float64
	sampler      string
	scheduler    string
	denoise      float64
	strength     float64
	refSampler   string
	refScheduler string
	refDenoise   float64
	refStrength  float64
	refSteps     int
	refCfg       float64
	varList      []string
	setList      []string
	images       []string
	masks        []string
	inputs       []string
	runAsync     bool
	runPlan      bool
	runRequestID string
	runTimeout   time.Duration
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a workflow",
	RunE:  runWorkflow,
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().StringVarP(&workflowName, "workflow", "w", "", "Workflow name or path (from workflows/ if bare)")
	runCmd.Flags().StringVar(&baseURL, "server", "", "Override ComfyUI server URL")
	runCmd.Flags().StringVarP(&outDir, "output", "o", "", "Output directory override")
	runCmd.Flags().StringVar(&outDir, "output-dir", "", "Output directory override (alias for --output)")
	runCmd.Flags().StringVar(&outputName, "output-name", "", "Set ${OUTPUT} for filename_prefix")
	runCmd.Flags().StringVar(&promptText, "prompt", "", "Set ${PROMPT}")
	runCmd.Flags().StringVar(&tagsText, "tags", "", "Set ${TAGS} (txt2music)")
	runCmd.Flags().StringVar(&lyricsText, "lyrics", "", "Set ${LYRICS} (txt2music)")
	runCmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "Set ${MAX_TOKENS} (VoxCPM2 audio length)")
	runCmd.Flags().IntVar(&seed, "seed", 0, "Set ${SEED}")
	runCmd.Flags().IntVar(&width, "width", 0, "Set ${WIDTH}")
	runCmd.Flags().IntVar(&height, "height", 0, "Set ${HEIGHT}")
	runCmd.Flags().IntVar(&steps, "steps", 0, "Set ${STEPS} and mapped sampler inputs")
	runCmd.Flags().Float64Var(&cfgScale, "cfg", 0, "Set ${CFG} and mapped sampler inputs")
	runCmd.Flags().StringVar(&sampler, "sampler", "", "Set sampler_name on sampler nodes")
	runCmd.Flags().StringVar(&scheduler, "scheduler", "", "Set scheduler on sampler nodes")
	runCmd.Flags().Float64Var(&denoise, "denoise", -1, "Set denoise on sampler nodes")
	runCmd.Flags().Float64Var(&strength, "strength", -1, "Set strength on nodes that support it")
	runCmd.Flags().StringVar(&refSampler, "refiner-sampler", "", "Set sampler_name on refiner sampler node")
	runCmd.Flags().StringVar(&refScheduler, "refiner-scheduler", "", "Set scheduler on refiner sampler node")
	runCmd.Flags().Float64Var(&refDenoise, "refiner-denoise", -1, "Set denoise on refiner sampler node")
	runCmd.Flags().Float64Var(&refStrength, "refiner-strength", -1, "Set strength on refiner nodes")
	runCmd.Flags().IntVar(&refSteps, "refiner-steps", 0, "Set steps on refiner node")
	runCmd.Flags().Float64Var(&refCfg, "refiner-cfg", 0, "Set cfg on refiner node")
	runCmd.Flags().StringArrayVar(&varList, "var", []string{}, "Template variable KEY=VALUE (repeatable)")
	runCmd.Flags().StringArrayVar(&setList, "set", []string{}, "Set <nodeID>.inputs.<name>=value (repeatable)")
	runCmd.Flags().StringArrayVar(&images, "image", []string{}, "Upload image and expose ${IMAGE}/${IMAGEn} (repeatable)")
	runCmd.Flags().StringArrayVar(&masks, "mask", []string{}, "Upload mask and expose ${MASK}/${MASKn} (repeatable)")
	runCmd.Flags().StringArrayVar(&inputs, "input", []string{}, "Upload input and expose ${INPUT}/${INPUTn} (repeatable)")
	runCmd.Flags().BoolVar(&runAsync, "async", false, "Submit and return without waiting")
	runCmd.Flags().BoolVar(&runPlan, "plan", false, "Resolve locally and validate required nodes against the target server without submitting")
	runCmd.Flags().StringVar(&runRequestID, "request-id", "", "Idempotency key for this submission")
	runCmd.Flags().DurationVar(&runTimeout, "timeout", 30*time.Minute, "Maximum wait time when not async")
}

func runWorkflow(command *cobra.Command, args []string) error {
	if workflowName == "--help" || workflowName == "-h" || workflowName == "help" {
		return command.Help()
	}
	selectedWorkflow := workflowName
	if selectedWorkflow == "" && len(args) > 0 {
		selectedWorkflow = args[0]
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if selectedWorkflow == "" {
		selectedWorkflow = cfg.DefaultWorkflow
	}
	if selectedWorkflow == "" {
		return errors.New("no workflow specified (-w) and no default_workflow in config")
	}
	if err := selectServer(cfg, baseURL); err != nil {
		return err
	}
	if outDir != "" {
		cfg.OutputDir = outDir
	}
	variables, err := runVariables(cfg)
	if err != nil {
		return err
	}
	parameters := runParameters()
	request := engine.Request{
		RequestID:  runRequestID,
		Workflow:   selectedWorkflow,
		Prompt:     promptText,
		Variables:  variables,
		Sets:       append([]string(nil), setList...),
		Parameters: parameters,
		Images:     append([]string(nil), images...),
		Masks:      append([]string(nil), masks...),
		Inputs:     append([]string(nil), inputs...),
		OutputDir:  cfg.OutputDir,
	}
	service := engine.New(engine.Options{Config: cfg})
	plan, err := service.Resolve(command.Context(), request)
	if err != nil {
		return err
	}
	if runPlan {
		client, err := configuredClient(cfg)
		if err != nil {
			return err
		}
		service = engine.New(engine.Options{Config: cfg, Client: client})
		serverValidation, err := service.ValidateServer(command.Context(), plan)
		if err != nil {
			return err
		}
		plan.ServerValidation = &serverValidation
		if err := emitJSON(plan); err != nil {
			return err
		}
		if !serverValidation.Valid {
			return Reported(fmt.Errorf("server is missing required node classes: %s", strings.Join(serverValidation.MissingNodeClasses, ", ")))
		}
		return nil
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
	service = engine.New(engine.Options{Config: cfg, Client: client, Jobs: store, OutputLimits: configuredOutputLimits(cfg)})
	humanf("Submitting workflow...\n")
	job, created, err := service.Submit(command.Context(), plan)
	if err != nil {
		return err
	}
	if !created {
		humanf("Reusing idempotent request %s\n", job.RequestID)
	}
	if runAsync {
		if machineJSON {
			return emitJSON(job)
		}
		humanf("Prompt ID: %s\n", job.PromptID)
		humanf("Submitted asynchronously. Use 'cmfy job wait --download %s' to collect outputs.\n", job.PromptID)
		return nil
	}
	if job.Status == "completed" && len(job.Outputs) > 0 {
		return renderRunResult(job)
	}
	ctx, cancel := context.WithTimeout(command.Context(), runTimeout)
	defer cancel()
	humanf("Waiting for completion...\n")
	job, err = service.Wait(ctx, job.PromptID, 1500*time.Millisecond)
	if err != nil {
		return err
	}
	if !isSuccessfulStatus(job.Status) {
		if job.Error != "" {
			return errors.New(job.Error)
		}
		return fmt.Errorf("prompt %s ended with status %s", job.PromptID, job.Status)
	}
	job, err = service.Collect(ctx, job.PromptID)
	if err != nil {
		return err
	}
	return renderRunResult(job)
}

func runVariables(cfg *config.Config) (map[string]string, error) {
	variables := map[string]string{}
	if outputName != "" {
		variables["OUTPUT"] = outputName
	} else if cfg.DefaultOutputName != "" {
		variables["OUTPUT"] = cfg.DefaultOutputName
	}
	if promptText != "" {
		variables["PROMPT"] = promptText
	}
	if tagsText != "" {
		variables["TAGS"] = tagsText
	}
	if lyricsText != "" {
		variables["LYRICS"] = lyricsText
	}
	if maxTokens != 0 {
		variables["MAX_TOKENS"] = fmt.Sprintf("%d", maxTokens)
	}
	resolvedSeed := seed
	if resolvedSeed == 0 {
		resolvedSeed = int(time.Now().UnixNano())
	}
	variables["SEED"] = fmt.Sprintf("%d", resolvedSeed)
	resolvedWidth := width
	if resolvedWidth == 0 {
		resolvedWidth = cfg.DefaultWidth
	}
	if resolvedWidth != 0 {
		variables["WIDTH"] = fmt.Sprintf("%d", resolvedWidth)
	}
	resolvedHeight := height
	if resolvedHeight == 0 {
		resolvedHeight = cfg.DefaultHeight
	}
	if resolvedHeight != 0 {
		variables["HEIGHT"] = fmt.Sprintf("%d", resolvedHeight)
	}
	resolvedSteps := steps
	if resolvedSteps == 0 {
		resolvedSteps = cfg.DefaultSteps
	}
	if resolvedSteps != 0 {
		variables["STEPS"] = fmt.Sprintf("%d", resolvedSteps)
	}
	if cfgScale != 0 {
		variables["CFG"] = trimFloat(cfgScale)
	}
	variables["FRAME_RATE"] = "25"
	variables["LENGTH"] = "121"
	for _, pair := range varList {
		key, value, ok := splitKV(pair)
		if !ok {
			return nil, fmt.Errorf("--var expects KEY=VALUE, got %q", pair)
		}
		variables[key] = value
	}
	return variables, nil
}

func runParameters() map[string]any {
	parameters := map[string]any{}
	setIfString := func(key, value string) {
		if value != "" {
			parameters[key] = value
		}
	}
	setIfFloat := func(key string, value float64) {
		if value >= 0 {
			parameters[key] = value
		}
	}
	setIfInt := func(key string, value int) {
		if value > 0 {
			parameters[key] = value
		}
	}
	setIfString("sampler_name", sampler)
	setIfString("scheduler", scheduler)
	setIfFloat("denoise", denoise)
	setIfFloat("strength", strength)
	setIfInt("steps", steps)
	if cfgScale > 0 {
		parameters["cfg"] = cfgScale
	}
	setIfString("refiner.sampler_name", refSampler)
	setIfString("refiner.scheduler", refScheduler)
	setIfFloat("refiner.denoise", refDenoise)
	setIfFloat("refiner.strength", refStrength)
	setIfInt("refiner.steps", refSteps)
	if refCfg > 0 {
		parameters["refiner.cfg"] = refCfg
	}
	return parameters
}

func renderRunResult(job jobs.Record) error {
	if machineJSON {
		return emitJSON(job)
	}
	humanf("Prompt ID: %s\n", job.PromptID)
	for _, asset := range job.Outputs {
		humanf("Saved: %s\n", asset.Path)
	}
	humanf("Workflow completed (%d file(s) saved)\n", len(job.Outputs))
	return nil
}

func isSuccessfulStatus(status string) bool {
	return status == "completed" || status == "success"
}
