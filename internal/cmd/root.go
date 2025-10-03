package cmd

import (
	"fmt"
	"os"

	"cmfy/internal/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const version = "0.1.0"

var cfgFile string

var rootCmd = &cobra.Command{
	Use:     "cmfy",
	Short:   "ComfyUI workflow runner",
	Version: version,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $XDG_CONFIG_HOME/cmfy/config.toml)")
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		p, err := config.Path()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error getting config path:", err)
			return
		}
		viper.SetConfigFile(p)
	}

	viper.SetConfigType("toml")
	viper.SetEnvPrefix("CMFY")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
	}
}
