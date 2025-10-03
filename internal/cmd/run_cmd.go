package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/workflow"

	"github.com/spf13/cobra"
)

var (
	workflowName string
	baseURL      string
	outDir       string
	outputName   string
	promptText   string
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
	runCmd.Flags().StringVar(&outputName, "output-name", "", "Convenience: sets ${OUTPUT} for filename_prefix")
	runCmd.Flags().StringVar(&promptText, "prompt", "", "Convenience: sets ${PROMPT}")
	runCmd.Flags().IntVar(&seed, "seed", 0, "Convenience: sets ${SEED}")
	runCmd.Flags().IntVar(&width, "width", 0, "Convenience: sets ${WIDTH}")
	runCmd.Flags().IntVar(&height, "height", 0, "Convenience: sets ${HEIGHT}")
	runCmd.Flags().IntVar(&steps, "steps", 0, "Convenience: sets ${STEPS} and sampler inputs if mapped")
	runCmd.Flags().Float64Var(&cfgScale, "cfg", 0, "Convenience: sets ${CFG} and sampler inputs if mapped")
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
	runCmd.Flags().StringArrayVar(&varList, "var", []string{}, "Template var override KEY=VAL (repeatable)")
	runCmd.Flags().StringArrayVar(&setList, "set", []string{}, "Set path=value at '<nodeID>.inputs.<name>' (repeatable)")
	runCmd.Flags().StringArrayVar(&images, "image", []string{}, "Upload image file and expose ${IMAGEn} (repeatable)")
	runCmd.Flags().StringArrayVar(&masks, "mask", []string{}, "Upload mask file and expose ${MASKn} (repeatable)")
	runCmd.Flags().StringArrayVar(&inputs, "input", []string{}, "Upload generic input file and expose ${INPUTn} (repeatable)")
}

func runWorkflow(cmd *cobra.Command, args []string) error {
	if workflowName == "--help" || workflowName == "-h" || workflowName == "help" {
		cmd.Help()
		return nil
	}

	if workflowName == "" && len(args) > 0 {
		workflowName = args[0]
	}

	if workflowName == "" {
		cfg, _ := config.Load()
		workflowName = cfg.DefaultWorkflow
		if workflowName == "" {
			return errors.New("no workflow specified (-w) and no default_workflow in config")
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if baseURL != "" {
		cfg.ServerURL = baseURL
	}
	if outDir != "" {
		cfg.OutputDir = outDir
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return err
	}

	aliasUsed := ""
	if wf, ok := resolveAliasMaybe(workflowName); ok {
		aliasUsed = workflowName
		workflowName = wf
	}
	prompt, wfPath, err := workflow.Load(cfg.WorkflowsDir, workflowName)
	if err != nil {
		return err
	}

	vars := map[string]string{}
	for k, v := range cfg.Vars {
		vars[k] = v
	}
	wfName := strings.TrimSuffix(filepath.Base(wfPath), filepath.Ext(wfPath))
	if wv, ok := cfg.WorkflowVars[wfName]; ok {
		for k, v := range wv {
			vars[k] = v
		}
	}
	if outputName != "" {
		vars["OUTPUT"] = outputName
	} else if cfg.DefaultOutputName != "" {
		vars["OUTPUT"] = cfg.DefaultOutputName
	}
	if promptText != "" {
		vars["PROMPT"] = promptText
	}
	if seed != 0 {
		vars["SEED"] = fmt.Sprintf("%d", seed)
	} else {
		vars["SEED"] = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if width != 0 {
		vars["WIDTH"] = fmt.Sprintf("%d", width)
	} else if cfg.DefaultWidth != 0 {
		vars["WIDTH"] = fmt.Sprintf("%d", cfg.DefaultWidth)
	}
	if height != 0 {
		vars["HEIGHT"] = fmt.Sprintf("%d", height)
	} else if cfg.DefaultHeight != 0 {
		vars["HEIGHT"] = fmt.Sprintf("%d", cfg.DefaultHeight)
	}
	if steps != 0 {
		vars["STEPS"] = fmt.Sprintf("%d", steps)
	} else if cfg.DefaultSteps != 0 {
		vars["STEPS"] = fmt.Sprintf("%d", cfg.DefaultSteps)
	}
	if cfgScale != 0 {
		vars["CFG"] = trimFloat(cfgScale)
	}
	for _, kv := range varList {
		k, v, ok := splitKV(kv)
		if !ok {
			return fmt.Errorf("--var expects KEY=VAL, got %q", kv)
		}
		vars[k] = v
	}

	client := comfy.NewClient(cfg.ServerURL)

	if len(images) > 0 {
		for i, p := range images {
			fmt.Printf("Uploading %s...\n", p)
			name, err := client.Upload(p)
			if err != nil {
				return fmt.Errorf("upload image %s: %w", p, err)
			}
			fmt.Printf("Uploaded as %s\n", name)
			vars[fmt.Sprintf("IMAGE%d", i+1)] = name
			if i == 0 {
				vars["IMAGE"] = name
			}
		}
	}
	if len(masks) > 0 {
		for i, p := range masks {
			name, err := client.Upload(p)
			if err != nil {
				return fmt.Errorf("upload mask %s: %w", p, err)
			}
			vars[fmt.Sprintf("MASK%d", i+1)] = name
			if i == 0 {
				vars["MASK"] = name
			}
		}
	}
	if len(inputs) > 0 {
		for i, p := range inputs {
			name, err := client.Upload(p)
			if err != nil {
				return fmt.Errorf("upload input %s: %w", p, err)
			}
			vars[fmt.Sprintf("INPUT%d", i+1)] = name
			if i == 0 {
				vars["INPUT"] = name
			}
		}
	}

	workflow.ApplyVars(prompt, vars)
	if err := workflow.ApplySets(prompt, setList); err != nil {
		return err
	}

	params := map[string]any{}
	if sampler != "" {
		params["sampler_name"] = sampler
	}
	if scheduler != "" {
		params["scheduler"] = scheduler
	}
	if denoise >= 0 {
		params["denoise"] = denoise
	}
	if strength >= 0 {
		params["strength"] = strength
	}
	if steps > 0 {
		params["steps"] = steps
	}
	if cfgScale > 0 {
		params["cfg"] = cfgScale
	}
	if refSampler != "" {
		params["refiner.sampler_name"] = refSampler
	}
	if refScheduler != "" {
		params["refiner.scheduler"] = refScheduler
	}
	if refDenoise >= 0 {
		params["refiner.denoise"] = refDenoise
	}
	if refStrength >= 0 {
		params["refiner.strength"] = refStrength
	}
	if refSteps > 0 {
		params["refiner.steps"] = refSteps
	}
	if refCfg > 0 {
		params["refiner.cfg"] = refCfg
	}
	if err := applyStandardParams(cfg, aliasUsed, prompt, params); err != nil {
		return err
	}

	clientID := fmt.Sprintf("cmfy-%d", time.Now().UnixNano())
	fmt.Println("Submitting workflow...")
	promptID, err := client.Prompt(clientID, prompt)
	if err != nil {
		return err
	}
	fmt.Printf("Prompt ID: %s\n", promptID)

	deadline := time.Now().Add(30 * time.Minute)
	lastState := ""
	fmt.Println("Waiting for completion...")
	for {
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for prompt to complete")
		}
		hist, err := client.History(promptID)
		if err != nil {
			return err
		}
		entry, _ := hist[promptID].(map[string]any)
		if entry == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		state := ""

		if statusVal, ok := entry["status"]; ok {
			switch v := statusVal.(type) {
			case string:
				state = v
			case map[string]any:
				if completed, ok := v["completed"].(bool); ok && completed {
					state = "completed"
				} else if statusStr, ok := v["status_str"].(string); ok {
					state = statusStr
				}
			}
		}

		outputs := getMap(entry, "outputs")
		if len(outputs) > 0 && state == "" {
			state = "completed"
		}

		if state != "" && state != lastState {
			fmt.Println("status:", state)
			lastState = state
		}
		if state == "completed" || state == "success" {
			if len(outputs) == 0 {
				fmt.Println("Workflow completed (no outputs to save)")
				break
			}
			saved := 0
			for nodeID, out := range outputs {
				om, ok := out.(map[string]any)
				if !ok {
					continue
				}
				images, _ := om["images"].([]any)
				for _, iv := range images {
					im, _ := iv.(map[string]any)
					filename := getString(im, "filename")
					subfolder := getString(im, "subfolder")
					typ := getString(im, "type")

					if filename == "" {
						fmt.Printf("Warning: empty filename in node %s output\n", nodeID)
						continue
					}

					data, err := client.View(filename, subfolder, typ)
					if err != nil {
						fmt.Printf("Warning: failed to fetch %s: %v\n", filename, err)
						continue
					}
					outPath := filepath.Join(cfg.OutputDir, filename)
					if err := os.WriteFile(outPath, data, 0o644); err != nil {
						return fmt.Errorf("failed to save %s: %w", outPath, err)
					}
					fmt.Println("Saved:", outPath)
					saved++
				}
			}
			if saved == 0 {
				fmt.Println("Workflow completed (no images saved)")
			} else {
				fmt.Printf("Workflow completed (%d image(s) saved)\n", saved)
			}
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}

	return nil
}
