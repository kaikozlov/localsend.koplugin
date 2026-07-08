package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"localsend-cli/cmd/nettest"
	"localsend-cli/cmd/recv"
	"localsend-cli/cmd/scan"
	"localsend-cli/cmd/send"
)

var rootCmd = &cobra.Command{
	Use:     "localsend",
	Short:   "LocalSend CLI",
	Long:    "LocalSend CLI",
	Version: versionString(),
}

func Execute() {
	rootCmd.SetVersionTemplate("{{ .Version }}\n")

	err := rootCmd.Execute()
	if err != nil {
		slog.Error("Fail to execute", "error", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(scan.Cmd)
	rootCmd.AddCommand(recv.Cmd)
	rootCmd.AddCommand(send.Cmd)
	rootCmd.AddCommand(nettest.Cmd)
}
