package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"cmfy/internal/config"
	"cmfy/internal/workflow"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	workflowsInspectShowGuidelines bool
	workflowContractVars           []string
)

var workflowsCmd = &cobra.Command{
	Use:   "workflows",
	Short: "Manage workflows",
}

var workflowsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available workflows",
	RunE:  workflowsList,
}

var workflowsShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show raw JSON for a workflow",
	Args:  cobra.ExactArgs(1),
	RunE:  workflowsShow,
}

var workflowsInspectCmd = &cobra.Command{
	Use:   "inspect <name>",
	Short: "Inspect workflow nodes and inputs",
	Args:  cobra.ExactArgs(1),
	RunE:  workflowsInspect,
}

var workflowsDescribeCmd = &cobra.Command{
	Use:   "describe <name>",
	Short: "Describe the deterministic workflow contract",
	Args:  cobra.ExactArgs(1),
	RunE:  workflowsDescribe,
}

var workflowsValidateCmd = &cobra.Command{
	Use:   "validate <name>",
	Short: "Validate required variables and connected outputs",
	Args:  cobra.ExactArgs(1),
	RunE:  workflowsValidate,
}

var workflowsAliasesCmd = &cobra.Command{
	Use:   "aliases",
	Short: "List workflow aliases",
	RunE:  workflowsAliases,
}

var workflowsAssignCmd = &cobra.Command{
	Use:   "assign <alias> <workflow>",
	Short: "Assign alias to workflow",
	Args:  cobra.ExactArgs(2),
	RunE:  workflowsAssign,
}

var workflowsAddCmd = &cobra.Command{
	Use:   "add <source.json> [name]",
	Short: "Add workflow with interactive variable setup",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  workflowsAdd,
}

func init() {
	rootCmd.AddCommand(workflowsCmd)
	workflowsCmd.AddCommand(workflowsListCmd)
	workflowsCmd.AddCommand(workflowsShowCmd)
	workflowsCmd.AddCommand(workflowsInspectCmd)
	workflowsCmd.AddCommand(workflowsDescribeCmd)
	workflowsCmd.AddCommand(workflowsValidateCmd)
	workflowsCmd.AddCommand(workflowsAliasesCmd)
	workflowsCmd.AddCommand(workflowsAssignCmd)
	workflowsCmd.AddCommand(workflowsAddCmd)
	workflowsInspectCmd.Flags().BoolVar(&workflowsInspectShowGuidelines, "guidelines", false, "Show optional prompt guidelines for this workflow")
	workflowsValidateCmd.Flags().StringArrayVar(&workflowContractVars, "var", nil, "Declare KEY=VALUE as available during validation")
}

func workflowsList(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	items, err := workflow.List(cfg.WorkflowsDir)
	if err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(items)
	}
	if len(items) == 0 {
		humanf("No workflows found in %s\n", cfg.WorkflowsDir)
		return nil
	}
	for _, n := range items {
		humanf("%s\n", n)
	}
	return nil
}

func workflowsShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	nameOrPath := args[0]
	if wf, ok := resolveAliasMaybe(nameOrPath); ok {
		nameOrPath = wf
	}
	p, resolved, err := workflow.Load(cfg.WorkflowsDir, nameOrPath)
	if err != nil {
		return err
	}
	out := map[string]any{"path": resolved, "prompt": p}
	if machineJSON {
		return emitJSON(out)
	}
	b, _ := json.MarshalIndent(map[string]any{"prompt": p}, "", "  ")
	fmt.Printf("# %s\n%s\n", resolved, string(b))
	return nil
}

func workflowsInspect(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	nameOrPath := args[0]
	if wf, ok := resolveAliasMaybe(nameOrPath); ok {
		nameOrPath = wf
	}
	pr, resolved, vars, err := workflow.LoadWithVars(cfg.WorkflowsDir, nameOrPath)
	if err != nil {
		return err
	}
	infos, _ := workflow.Inspect(pr)
	if machineJSON {
		contract, err := workflow.Describe(pr, vars)
		if err != nil {
			return err
		}
		return emitJSON(map[string]any{"path": resolved, "contract": contract, "nodes": infos})
	}
	fmt.Printf("# %s\n", resolved)

	if workflowsInspectShowGuidelines {
		pg, _, err := workflow.LoadPromptGuidelines(cfg.WorkflowsDir, nameOrPath)
		if err != nil {
			return err
		}
		fmt.Println()
		if pg == nil {
			fmt.Println("Prompt guidelines: none")
		} else {
			printPromptGuidelines(pg)
		}
	}

	if len(vars) > 0 {
		fmt.Println("\nVariables:")
		varNames := make([]string, 0, len(vars))
		for k := range vars {
			varNames = append(varNames, k)
		}
		sort.Strings(varNames)
		for _, k := range varNames {
			v := vars[k]
			if v.Description != "" {
				fmt.Printf("  %s = %q (%s)\n", k, v.Default, v.Description)
			} else {
				fmt.Printf("  %s = %q\n", k, v.Default)
			}
		}
		fmt.Println()
	}

	fmt.Println("Nodes:")
	for _, n := range infos {
		fmt.Printf("  %s: %s\n", n.ID, n.ClassType)
		if len(n.Inputs) > 0 {
			fmt.Printf("    inputs: %s\n", strings.Join(n.Inputs, ", "))
		}
	}
	return nil
}

func workflowsDescribe(_ *cobra.Command, args []string) error {
	prompt, resolved, variables, err := loadWorkflowForContract(args[0])
	if err != nil {
		return err
	}
	contract, err := workflow.Describe(prompt, variables)
	if err != nil {
		return err
	}
	result := map[string]any{"schema": "cmfy/workflow-description-v1", "path": resolved, "contract": contract}
	if machineJSON {
		return emitJSON(result)
	}
	return emitJSON(result)
}

func workflowsValidate(_ *cobra.Command, args []string) error {
	prompt, resolved, defaults, err := loadWorkflowForContract(args[0])
	if err != nil {
		return err
	}
	values := map[string]string{}
	for name, metadata := range defaults {
		if metadata.Default != "" {
			values[name] = metadata.Default
		}
	}
	for _, pair := range workflowContractVars {
		key, value, ok := splitKV(pair)
		if !ok {
			return fmt.Errorf("--var expects KEY=VALUE, got %q", pair)
		}
		values[key] = value
	}
	validation, err := workflow.Validate(prompt, values)
	if err != nil {
		return err
	}
	result := map[string]any{"schema": "cmfy/workflow-validation-v1", "path": resolved, "validation": validation}
	if err := emitJSON(result); err != nil {
		return err
	}
	if !validation.Valid {
		err := errors.New("workflow validation failed")
		if machineJSON {
			return Reported(err)
		}
		return err
	}
	return nil
}

func loadWorkflowForContract(name string) (map[string]any, string, map[string]workflow.VariableMetadata, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", nil, err
	}
	if resolved, ok := resolveAliasMaybe(name); ok {
		name = resolved
	}
	prompt, path, variables, err := workflow.LoadWithVars(cfg.WorkflowsDir, name)
	if err != nil {
		return nil, "", nil, err
	}
	delete(prompt, "#variables")
	delete(prompt, "variables")
	delete(prompt, "prompt_guidelines")
	delete(prompt, "guidelines")
	return prompt, path, variables, nil
}

func printPromptGuidelines(pg *workflow.PromptGuidelines) {
	fmt.Println("Prompt guidelines:")
	if pg.Summary != "" {
		fmt.Println("  Summary:", pg.Summary)
	}
	if pg.Style != "" {
		fmt.Println("  Style:", pg.Style)
	}
	printGuidelineSection("Do", pg.Dos)
	printGuidelineSection("Avoid", pg.Donts)
	printGuidelineSection("Keywords", pg.Keywords)
	printGuidelineSection("Structure", pg.Structure)
	printGuidelineSection("Examples", pg.Examples)
	printGuidelineSection("Notes", pg.Notes)
}

func printGuidelineSection(title string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("  %s:\n", title)
	for _, item := range items {
		fmt.Printf("    - %s\n", item)
	}
}

func workflowsAliases(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	known := []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2music", "txt2img_lora", "img2img_inpainting"}
	seen := map[string]bool{}
	for _, k := range known {
		seen[k] = true
	}
	for k := range cfg.StandardWorkflows {
		seen[k] = true
	}
	aliases := make([]string, 0, len(seen))
	for alias := range seen {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	results := make([]map[string]any, 0, len(aliases))
	for _, alias := range aliases {
		target := strings.TrimSpace(cfg.StandardWorkflows[alias])
		implicit := false
		if target == "" {
			if _, _, err := workflow.Load(cfg.WorkflowsDir, alias); err == nil {
				target = alias
				implicit = true
			}
		}
		results = append(results, map[string]any{"alias": alias, "target": target, "implicit": implicit})
	}
	if machineJSON {
		return emitJSON(results)
	}
	for _, result := range results {
		if result["target"] == "" {
			humanf("%s -> <unset>\n", result["alias"])
		} else if result["implicit"] == true {
			humanf("%s -> %s (implicit)\n", result["alias"], result["target"])
		} else {
			humanf("%s -> %s\n", result["alias"], result["target"])
		}
	}
	return nil
}

func workflowsAssign(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	alias := args[0]
	wf := args[1]
	if cfg.StandardWorkflows == nil {
		cfg.StandardWorkflows = map[string]string{}
	}
	cfg.StandardWorkflows[alias] = wf
	if err := config.Save(cfg); err != nil {
		return err
	}
	if machineJSON {
		return emitJSON(map[string]any{"schema": "cmfy/workflow-alias-v1", "alias": alias, "target": wf})
	}
	humanf("Assigned %s -> %s\n", alias, wf)
	return nil
}

func workflowsAdd(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	sourcePath := args[0]
	var targetName string
	if len(args) > 1 {
		targetName = args[1]
	} else {
		targetName = strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
	}

	if !strings.HasSuffix(targetName, ".json") {
		targetName += ".json"
	}

	prompt, _, err := workflow.Load("", sourcePath)
	if err != nil {
		return err
	}

	candidates := workflow.SuggestVariables(prompt)

	if len(candidates) == 0 {
		fmt.Println("No variable candidates found in workflow")
		targetPath := filepath.Join(cfg.WorkflowsDir, targetName)
		if err := workflow.Save(targetPath, prompt, nil); err != nil {
			return err
		}
		fmt.Printf("Workflow saved to %s\n", targetPath)
		return nil
	}

	fmt.Printf("Found %d potential variables\n\n", len(candidates))

	vars := make(map[string]workflow.VariableMetadata)

	for _, c := range candidates {
		fmt.Printf("Node %s (%s) input %q\n", c.NodeID, c.ClassType, c.InputName)
		fmt.Printf("Current value: %v\n", c.CurrentValue)

		convertPrompt := promptui.Prompt{
			Label:   "Convert to variable? (y/n)",
			Default: "y",
		}

		result, err := convertPrompt.Run()
		if err != nil {
			fmt.Println()
			continue
		}
		result = strings.ToLower(strings.TrimSpace(result))
		if result == "n" || result == "no" {
			fmt.Println()
			continue
		}

		varNamePrompt := promptui.Prompt{
			Label:   "Variable name",
			Default: c.SuggestedVar,
		}

		varName, err := varNamePrompt.Run()
		if err != nil {
			return err
		}

		defaultValue := fmt.Sprintf("%v", c.CurrentValue)
		defaultPrompt := promptui.Prompt{
			Label:   "Default value",
			Default: defaultValue,
		}

		defaultVal, err := defaultPrompt.Run()
		if err != nil {
			return err
		}

		descPrompt := promptui.Prompt{
			Label: "Description (optional)",
		}

		desc, _ := descPrompt.Run()

		vars[varName] = workflow.VariableMetadata{
			Default:     defaultVal,
			Description: desc,
		}

		if err := workflow.SetPath(prompt, fmt.Sprintf("%s.inputs.%s", c.NodeID, c.InputName), fmt.Sprintf("${%s}", varName)); err != nil {
			return err
		}

		fmt.Println()
	}

	targetPath := filepath.Join(cfg.WorkflowsDir, targetName)
	if err := workflow.Save(targetPath, prompt, vars); err != nil {
		return err
	}

	fmt.Printf("Workflow saved to %s with %d variables\n", targetPath, len(vars))
	return nil
}
