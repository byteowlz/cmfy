package main

import (
	"fmt"
	"os"

	"cmfy/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if cmd.MachineJSONEnabled() {
			if !cmd.IsReported(err) {
				_ = cmd.WriteMachineError(err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		os.Exit(1)
	}
}
