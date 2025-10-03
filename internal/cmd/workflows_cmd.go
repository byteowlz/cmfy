package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"cmfy/internal/config"
	"cmfy/internal/workflow"

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

func init() {
	rootCmd.AddCommand(workflowsCmd)
	workflowsCmd.AddCommand(workflowsListCmd)
	workflowsCmd.AddCommand(workflowsShowCmd)
	workflowsCmd.AddCommand(workflowsInspectCmd)
	workflowsCmd.AddCommand(workflowsAliasesCmd)
	workflowsCmd.AddCommand(workflowsAssignCmd)
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
