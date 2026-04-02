package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "wpswag",
	Short: "Generate OpenAPI 3.0 specs from WordPress REST APIs",
	Long:  "wpswag converts a WordPress REST API index or namespace JSON into an OpenAPI 3.0.3 specification.",
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
