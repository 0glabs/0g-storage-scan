package cmd

import (
	nhApi "github.com/0glabs/0g-storage-scan/api"
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/spf13/cobra"
)

var (
	daAPICmd = &cobra.Command{
		Use:   "da_api",
		Short: "run rest da api server",
		Run:   startDAAPIService,
	}
)

func init() {
	rootCmd.AddCommand(daAPICmd)
}

func startDAAPIService(*cobra.Command, []string) {
	dataCtx := MustInitDataContext()
	defer dataCtx.Close()

	nhApi.MustInit(dataCtx.Eth, dataCtx.DB)

	api.MustServeFromViper(nhApi.RegisterDARouter)
}
