package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/workflow"
)

func printRunUsage() {
	fmt.Println("Usage: cmfy run [options]")
	fmt.Println("\nOptions:")
	fmt.Println("  -w <workflow>              Workflow name or path")
	fmt.Println("  --server <url>             Override ComfyUI server URL")
	fmt.Println("  -o <dir>                   Output directory override")
	fmt.Println("  --prompt <text>            Set ${PROMPT}")
	fmt.Println("  --seed <int>               Set ${SEED}")
	fmt.Println("  --width <int>              Set ${WIDTH}")
	fmt.Println("  --height <int>             Set ${HEIGHT}")
	fmt.Println("  --steps <int>              Set ${STEPS}")
	fmt.Println("  --cfg <float>              Set ${CFG}")
	fmt.Println("  --sampler <name>           Set sampler_name on sampler nodes")
	fmt.Println("  --scheduler <name>         Set scheduler on sampler nodes")
	fmt.Println("  --denoise <float>          Set denoise on sampler nodes")
	fmt.Println("  --strength <float>         Set strength on nodes")
	fmt.Println("  --refiner-sampler <name>   Set sampler_name on refiner node")
	fmt.Println("  --refiner-scheduler <name> Set scheduler on refiner node")
	fmt.Println("  --refiner-denoise <float>  Set denoise on refiner node")
	fmt.Println("  --refiner-strength <float> Set strength on refiner node")
	fmt.Println("  --refiner-steps <int>      Set steps on refiner node")
	fmt.Println("  --refiner-cfg <float>      Set cfg on refiner node")
	fmt.Println("  --var KEY=VAL              Template variable override (repeatable)")
	fmt.Println("  --set path=value           Set at '<nodeID>.inputs.<name>' (repeatable)")
	fmt.Println("  --image <path>             Upload image and expose ${IMAGEn} (repeatable)")
	fmt.Println("  --mask <path>              Upload mask and expose ${MASKn} (repeatable)")
	fmt.Println("  --input <path>             Upload input and expose ${INPUTn} (repeatable)")
}

// Run executes a workflow using flags.
func Run(args []string) error {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			printRunUsage()
			return nil
		}
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	workflowName := fs.String("w", "", "Workflow name or path (from workflows/ if bare)")
	baseURL := fs.String("server", "", "Override ComfyUI server URL")
	outDir := fs.String("o", "", "Output directory override")
	promptText := fs.String("prompt", "", "Convenience: sets ${PROMPT}")
	seed := fs.Int("seed", 0, "Convenience: sets ${SEED}")
	width := fs.Int("width", 0, "Convenience: sets ${WIDTH}")
	height := fs.Int("height", 0, "Convenience: sets ${HEIGHT}")
	steps := fs.Int("steps", 0, "Convenience: sets ${STEPS} and sampler inputs if mapped")
	cfgScale := fs.Float64("cfg", 0, "Convenience: sets ${CFG} and sampler inputs if mapped")
	sampler := fs.String("sampler", "", "Set sampler_name on sampler nodes")
	scheduler := fs.String("scheduler", "", "Set scheduler on sampler nodes")
	denoise := fs.Float64("denoise", -1, "Set denoise on sampler nodes")
	strength := fs.Float64("strength", -1, "Set strength on nodes that support it")
	refSampler := fs.String("refiner-sampler", "", "Set sampler_name on refiner sampler node")
	refScheduler := fs.String("refiner-scheduler", "", "Set scheduler on refiner sampler node")
	refDenoise := fs.Float64("refiner-denoise", -1, "Set denoise on refiner sampler node")
	refStrength := fs.Float64("refiner-strength", -1, "Set strength on refiner nodes")
	refSteps := fs.Int("refiner-steps", 0, "Set steps on refiner node")
	refCfg := fs.Float64("refiner-cfg", 0, "Set cfg on refiner node")
	var varList multiVal
	fs.Var(&varList, "var", "Template var override KEY=VAL (repeatable)")
	var setList multiVal
	fs.Var(&setList, "set", "Set path=value at '<nodeID>.inputs.<name>' (repeatable)")
	var images multiVal
	fs.Var(&images, "image", "Upload image file and expose ${IMAGEn} (repeatable)")
	var masks multiVal
	fs.Var(&masks, "mask", "Upload mask file and expose ${MASKn} (repeatable)")
	var inputs multiVal
	fs.Var(&inputs, "input", "Upload generic input file and expose ${INPUTn} (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Check if first positional argument could be a workflow name
	if *workflowName == "" && len(fs.Args()) > 0 {
		*workflowName = fs.Args()[0]
	}

	if *workflowName == "" {
		// fallback to config default
		cfg, _ := config.Load()
		*workflowName = cfg.DefaultWorkflow
		if *workflowName == "" {
			return errors.New("no workflow specified (-w) and no default_workflow in config")
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *baseURL != "" {
		cfg.ServerURL = *baseURL
	}
	if *outDir != "" {
		cfg.OutputDir = *outDir
	}
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return err
	}

	// Allow alias mapping resolution: if name matches alias, resolve
	aliasUsed := ""
	if wf, ok := resolveAliasMaybe(*workflowName); ok {
		aliasUsed = *workflowName
		*workflowName = wf
	}
	prompt, wfPath, err := workflow.Load(cfg.WorkflowsDir, *workflowName)
	if err != nil {
		return err
	}

	// Build var map: defaults -> per-workflow -> CLI convenience -> --var
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
	if *promptText != "" {
		vars["PROMPT"] = *promptText
	}
	if *seed != 0 {
		vars["SEED"] = fmt.Sprintf("%d", *seed)
	}
	if *width != 0 {
		vars["WIDTH"] = fmt.Sprintf("%d", *width)
	}
	if *height != 0 {
		vars["HEIGHT"] = fmt.Sprintf("%d", *height)
	}
	if *steps != 0 {
		vars["STEPS"] = fmt.Sprintf("%d", *steps)
	}
	if *cfgScale != 0 {
		vars["CFG"] = trimFloat(*cfgScale)
	}
	for _, kv := range varList {
		k, v, ok := splitKV(kv)
		if !ok {
			return fmt.Errorf("--var expects KEY=VAL, got %q", kv)
		}
		vars[k] = v
	}

	client := comfy.NewClient(cfg.ServerURL)

	// Upload assets and populate variables
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

	// Apply vars to prompt
	workflow.ApplyVars(prompt, vars)
	// Apply explicit sets
	if err := workflow.ApplySets(prompt, setList); err != nil {
		return err
	}

	// Apply first-class sampler/params via config mapping or heuristics
	params := map[string]interface{}{}
	if *sampler != "" {
		params["sampler_name"] = *sampler
	}
	if *scheduler != "" {
		params["scheduler"] = *scheduler
	}
	if *denoise >= 0 {
		params["denoise"] = *denoise
	}
	if *strength >= 0 {
		params["strength"] = *strength
	}
	if *steps > 0 {
		params["steps"] = *steps
	}
	if *cfgScale > 0 {
		params["cfg"] = *cfgScale
	}
	if *refSampler != "" {
		params["refiner.sampler_name"] = *refSampler
	}
	if *refScheduler != "" {
		params["refiner.scheduler"] = *refScheduler
	}
	if *refDenoise >= 0 {
		params["refiner.denoise"] = *refDenoise
	}
	if *refStrength >= 0 {
		params["refiner.strength"] = *refStrength
	}
	if *refSteps > 0 {
		params["refiner.steps"] = *refSteps
	}
	if *refCfg > 0 {
		params["refiner.cfg"] = *refCfg
	}
	if err := applyStandardParams(cfg, aliasUsed, prompt, params); err != nil {
		return err
	}

	// Submit
	clientID := fmt.Sprintf("cmfy-%d", time.Now().UnixNano())
	fmt.Println("Submitting workflow...")
	promptID, err := client.Prompt(clientID, prompt)
	if err != nil {
		return err
	}
	fmt.Printf("Prompt ID: %s\n", promptID)

	// Poll history until done
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
		// The history response is a map keyed by prompt_id
		entry, _ := hist[promptID].(map[string]interface{})
		if entry == nil {
			time.Sleep(1 * time.Second)
			continue
		}

		// Handle different status formats from ComfyUI
		state := ""

		// Try string status first
		if statusVal, ok := entry["status"]; ok {
			switch v := statusVal.(type) {
			case string:
				state = v
			case map[string]interface{}:
				// Status might be an object with completion info
				if completed, ok := v["completed"].(bool); ok && completed {
					state = "completed"
				} else if statusStr, ok := v["status_str"].(string); ok {
					state = statusStr
				}
			}
		}

		// Check if outputs exist - reliable indicator of completion
		outputs := getMap(entry, "outputs")
		if len(outputs) > 0 && state == "" {
			state = "completed"
		}

		if state != "" && state != lastState {
			fmt.Println("status:", state)
			lastState = state
		}
		if state == "completed" || state == "success" {
			// Collect outputs - already fetched above
			if len(outputs) == 0 {
				fmt.Println("Workflow completed (no outputs to save)")
				break
			}
			saved := 0
			for nodeID, out := range outputs {
				om, ok := out.(map[string]interface{})
				if !ok {
					continue
				}
				images, _ := om["images"].([]interface{})
				for _, iv := range images {
					im, _ := iv.(map[string]interface{})
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

type multiVal []string

func (m *multiVal) String() string     { return strings.Join(*m, ",") }
func (m *multiVal) Set(v string) error { *m = append(*m, v); return nil }

func splitKV(s string) (string, string, bool) {
	eq := strings.Index(s, "=")
	if eq <= 0 {
		return "", "", false
	}
	return s[:eq], s[eq+1:], true
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.6f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func getString(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k]; ok {
		switch t := v.(type) {
		case string:
			return t
		case json.Number:
			return t.String()
		}
	}
	return ""
}

func getMap(m map[string]interface{}, k string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[k].(map[string]interface{}); ok {
		return v
	}
	return nil
}

// WorkflowsList prints available workflows.
func WorkflowsList() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	items, err := workflow.List(cfg.WorkflowsDir)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Println("No workflows found in", cfg.WorkflowsDir)
		return nil
	}
	for _, n := range items {
		fmt.Println(n)
	}
	return nil
}

// WorkflowsShow prints the raw JSON for a workflow.
func WorkflowsShow(nameOrPath string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Allow alias resolution
	if wf, ok := resolveAliasMaybe(nameOrPath); ok {
		nameOrPath = wf
	}
	p, resolved, err := workflow.Load(cfg.WorkflowsDir, nameOrPath)
	if err != nil {
		return err
	}
	// reconstruct into wrapper for readability
	out := map[string]interface{}{"prompt": p}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Printf("# %s\n%s\n", resolved, string(b))
	return nil
}

// ServerPing checks connectivity to ComfyUI server.
func ServerPing(urlOverride string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if urlOverride != "" {
		cfg.ServerURL = urlOverride
	}
	c := comfy.NewClient(cfg.ServerURL)
	if err := c.Ping(); err != nil {
		return err
	}
	fmt.Println("OK:", cfg.ServerURL)
	return nil
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Resolve alias name into configured workflow string, falling back
// to alias name if a matching file exists under workflows_dir.
func ResolveAlias(alias string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if v := cfg.StandardWorkflows[alias]; strings.TrimSpace(v) != "" {
		return v, nil
	}
	// If workflows_dir/<alias>.json exists, permit using alias directly
	if _, _, err := workflow.Load(cfg.WorkflowsDir, alias); err == nil {
		return alias, nil
	}
	return "", fmt.Errorf("alias %q is not assigned; set with 'cmfy workflows assign %s <workflow>'", alias, alias)
}

func resolveAliasMaybe(name string) (string, bool) {
	cfg, err := config.Load()
	if err != nil {
		return "", false
	}
	if v := cfg.StandardWorkflows[name]; strings.TrimSpace(v) != "" {
		return v, true
	}
	return "", false
}

// WorkflowsAliases prints alias -> workflow mapping, indicating resolution.
func WorkflowsAliases() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Collect from config and known defaults
	known := []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2img_lora", "img2img_inpainting"}
	seen := map[string]bool{}
	for _, k := range known {
		seen[k] = true
	}
	for k := range cfg.StandardWorkflows {
		seen[k] = true
	}
	// Determine resolution
	for alias := range seen {
		v := cfg.StandardWorkflows[alias]
		if strings.TrimSpace(v) == "" {
			v = ""
		}
		if v == "" {
			// fallback if file exists
			if _, _, err := workflow.Load(cfg.WorkflowsDir, alias); err == nil {
				fmt.Printf("%s -> %s (implicit)\n", alias, alias)
				continue
			}
			fmt.Printf("%s -> <unset>\n", alias)
		} else {
			fmt.Printf("%s -> %s\n", alias, v)
		}
	}
	return nil
}

// WorkflowsAssign sets alias -> workflow mapping and saves config.
func WorkflowsAssign(alias, wf string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.StandardWorkflows == nil {
		cfg.StandardWorkflows = map[string]string{}
	}
	cfg.StandardWorkflows[alias] = wf
	if err := config.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("Assigned %s -> %s\n", alias, wf)
	return nil
}

// WorkflowsInspect prints node IDs, class types, and input names.
func WorkflowsInspect(nameOrPath string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Allow alias resolution
	if wf, ok := resolveAliasMaybe(nameOrPath); ok {
		nameOrPath = wf
	}
	pr, resolved, err := workflow.Load(cfg.WorkflowsDir, nameOrPath)
	if err != nil {
		return err
	}
	infos, _ := workflow.Inspect(pr)
	fmt.Printf("# %s\n", resolved)
	for _, n := range infos {
		fmt.Printf("%s: %s\n", n.ID, n.ClassType)
		if len(n.Inputs) > 0 {
			fmt.Printf("  inputs: %s\n", strings.Join(n.Inputs, ", "))
		}
	}
	return nil
}

// applyStandardParams sets well-known parameters using config mappings or heuristics.
func applyStandardParams(cfg *config.Config, alias string, prompt map[string]interface{}, params map[string]interface{}) error {
	// Try config mappings first if alias provided
	if alias != "" {
		if m := cfg.StandardWorkflowParams[alias]; len(m) > 0 {
			for k, v := range params {
				if pth, ok := m[k]; ok {
					if err := workflow.SetPath(prompt, pth, v); err != nil {
						return err
					}
					delete(params, k)
				}
			}
		}
	}
	// Heuristics: for base params, set first occurrence; for refiner.*, set second occurrence
	for k, v := range params {
		occ := 0
		key := k
		if strings.HasPrefix(k, "refiner.") {
			key = strings.TrimPrefix(k, "refiner.")
			occ = 1
		}
		_ = workflow.SetFirstByInput(prompt, key, v, occ)
	}
	return nil
}
