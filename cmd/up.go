package cmd

import (
	"log"

	"github.com/jbrekelmans/kube-compose/pkg/up"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "A brief description of your command",
	Long:  "creates pods and services in an order that respects depends_on in the docker compose file",
	Run:   upCommand,
}

func upCommand(cmd *cobra.Command, args []string) {
	cfg, err := newConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cfg.EnvironmentID, _ = cmd.Flags().GetString("env-id")
	if x, _ := cmd.Flags().GetString("namespace"); x != "" {
		cfg.Namespace, _ = cmd.Flags().GetString("namespace")
	}
	cfg.Services = args
	cfg.Detach, _ = cmd.Flags().GetBool("detach")
	err = up.Run(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func init() {
	rootCmd.AddCommand(upCmd)
	upCmd.PersistentFlags().BoolP("detach", "d", false, "Detach mode")
}
