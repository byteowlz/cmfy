package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
)

var aliases = []string{"txt2img", "img2img", "canny2img", "depth2img", "img2vid", "txt2vid", "txt2img_lora", "img2img_inpainting", "rmb"}

func init() {
	for _, alias := range aliases {
		createAliasCommand(alias)
	}
}

func createAliasCommand(alias string) {
	cmd := &cobra.Command{
		Use:   alias,
		Short: fmt.Sprintf("Run %s workflow", alias),
		RunE: func(cmd *cobra.Command, args []string) error {
			wf, err := ResolveAlias(alias)
			if err != nil {
				return err
			}
			workflowName = filepath.Clean(wf)
			return runWorkflow(cmd, args)
		},
	}

	cmd.Flags().StringVar(&baseURL, "server", "", "Override ComfyUI server URL")
	cmd.Flags().StringVarP(&outDir, "output", "o", "", "Output directory override")
	cmd.Flags().StringVar(&promptText, "prompt", "", "Convenience: sets ${PROMPT}")
	cmd.Flags().IntVar(&seed, "seed", 0, "Convenience: sets ${SEED}")
	cmd.Flags().IntVar(&width, "width", 0, "Convenience: sets ${WIDTH}")
	cmd.Flags().IntVar(&height, "height", 0, "Convenience: sets ${HEIGHT}")
	cmd.Flags().IntVar(&steps, "steps", 0, "Convenience: sets ${STEPS} and sampler inputs if mapped")
	cmd.Flags().Float64Var(&cfgScale, "cfg", 0, "Convenience: sets ${CFG} and sampler inputs if mapped")
	cmd.Flags().StringVar(&sampler, "sampler", "", "Set sampler_name on sampler nodes")
	cmd.Flags().StringVar(&scheduler, "scheduler", "", "Set scheduler on sampler nodes")
	cmd.Flags().Float64Var(&denoise, "denoise", -1, "Set denoise on sampler nodes")
	cmd.Flags().Float64Var(&strength, "strength", -1, "Set strength on nodes that support it")
	cmd.Flags().StringVar(&refSampler, "refiner-sampler", "", "Set sampler_name on refiner sampler node")
	cmd.Flags().StringVar(&refScheduler, "refiner-scheduler", "", "Set scheduler on refiner sampler node")
	cmd.Flags().Float64Var(&refDenoise, "refiner-denoise", -1, "Set denoise on refiner sampler node")
	cmd.Flags().Float64Var(&refStrength, "refiner-strength", -1, "Set strength on refiner nodes")
	cmd.Flags().IntVar(&refSteps, "refiner-steps", 0, "Set steps on refiner node")
	cmd.Flags().Float64Var(&refCfg, "refiner-cfg", 0, "Set cfg on refiner node")
	cmd.Flags().StringArrayVar(&varList, "var", []string{}, "Template var override KEY=VAL (repeatable)")
	cmd.Flags().StringArrayVar(&setList, "set", []string{}, "Set path=value at '<nodeID>.inputs.<name>' (repeatable)")
	cmd.Flags().StringArrayVar(&images, "image", []string{}, "Upload image file and expose ${IMAGEn} (repeatable)")
	cmd.Flags().StringArrayVar(&masks, "mask", []string{}, "Upload mask file and expose ${MASKn} (repeatable)")
	cmd.Flags().StringArrayVar(&inputs, "input", []string{}, "Upload generic input file and expose ${INPUTn} (repeatable)")

	rootCmd.AddCommand(cmd)
}
