package cmd

import (
	"log"

	"github.com/jbrekelmans/kube-compose/pkg/down"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "A brief description of your command",
	Long:  "destroy all pods and services",
	Run:   downCommand,
}

func downCommand(cmd *cobra.Command, _ []string) {
	cfg, err := newConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cfg.EnvironmentID, _ = cmd.Flags().GetString("env-id")
	if x, _ := cmd.Flags().GetString("namespace"); x != "" {
		cfg.Namespace, _ = cmd.Flags().GetString("namespace")
	}
	err = down.Run(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

// This method is generated when cobra is initialized.
// Flags and configuration settings are meant to be
// configured here.
// nolint
func init() {
	rootCmd.AddCommand(downCmd)
}
