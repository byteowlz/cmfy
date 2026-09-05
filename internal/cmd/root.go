package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"cmfy/internal/config"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// version is set at build time via ldflags: -X cmfy/internal/cmd.version=vX.Y.Z
var version = "0.2.4"

var (
	cfgFile       string
	machineJSON   bool
	quiet         bool
	stateDir      string
	serverProfile string
)

var rootCmd = &cobra.Command{
	Use:           "cmfy",
	Short:         "ComfyUI workflow runner",
	Version:       version,
	SilenceErrors: true,
	SilenceUsage:  true,
}

type reportedError struct{ err error }

func (e reportedError) Error() string { return e.err.Error() }
func (e reportedError) Unwrap() error { return e.err }

func Execute() error           { return rootCmd.Execute() }
func Reported(err error) error { return reportedError{err: err} }
func MachineJSONEnabled() bool { return machineJSON }

func IsReported(err error) bool {
	var target reportedError
	return errors.As(err, &target)
}

func WriteMachineError(err error) error {
	code := "operation_failed"
	message := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout"):
		code = "timeout"
	case errors.Is(err, os.ErrNotExist) || strings.Contains(message, "not found"):
		code = "not_found"
	case strings.Contains(message, "unresolved variables") || strings.Contains(message, "workflow validation"):
		code = "workflow_invalid"
	case strings.Contains(message, "required") || strings.Contains(message, "invalid") || strings.Contains(message, "expects"):
		code = "invalid_argument"
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"schema": "cmfy/error-v1",
		"error":  map[string]string{"code": code, "message": err.Error()},
	})
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $XDG_CONFIG_HOME/cmfy/config.toml)")
	rootCmd.PersistentFlags().BoolVar(&machineJSON, "json", false, "Emit one machine-readable JSON value on stdout")
	rootCmd.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress human progress output")
	rootCmd.PersistentFlags().StringVar(&stateDir, "state-dir", "", "Job state directory (default: $CMFY_STATE_DIR or $XDG_STATE_HOME/cmfy)")
	rootCmd.PersistentFlags().StringVar(&serverProfile, "profile", "", "Named [servers.<name>] profile from config")
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}

func initConfig() {
	if cfgFile != "" {
		_ = os.Setenv("CMFY_CONFIG", cfgFile)
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
