package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"cmfy/internal/config"
	"cmfy/internal/workflow"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
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
	workflowsCmd.AddCommand(workflowsAliasesCmd)
	workflowsCmd.AddCommand(workflowsAssignCmd)
	workflowsCmd.AddCommand(workflowsAddCmd)
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
	if len(items) == 0 {
		fmt.Println("No workflows found in", cfg.WorkflowsDir)
		return nil
	}
	for _, n := range items {
		fmt.Println(n)
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
	out := map[string]any{"prompt": p}
	b, _ := json.MarshalIndent(out, "", "  ")
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
	fmt.Printf("# %s\n", resolved)

	if len(vars) > 0 {
		fmt.Println("\nVariables:")
		varNames := make([]string, 0, len(vars))
		for k := range vars {
			varNames = append(varNames, k)
		}
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

func workflowsAliases(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	known := []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2img_lora", "img2img_inpainting"}
	seen := map[string]bool{}
	for _, k := range known {
		seen[k] = true
	}
	for k := range cfg.StandardWorkflows {
		seen[k] = true
	}
	for alias := range seen {
		v := cfg.StandardWorkflows[alias]
		if strings.TrimSpace(v) == "" {
			v = ""
		}
		if v == "" {
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
	fmt.Printf("Assigned %s -> %s\n", alias, wf)
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
