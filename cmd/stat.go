package cmd

import (
	"context"
	"sync"

	"github.com/0glabs/0g-storage-scan/stat"
	"github.com/Conflux-Chain/go-conflux-util/viper"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	statCmd = &cobra.Command{
		Use:   "stat",
		Short: "Start stat, including transactions and data size of storage",
		Run:   startStatService,
	}
)

func init() {
	rootCmd.AddCommand(statCmd)
}

func startStatService(*cobra.Command, []string) {
	logrus.Info("Start to stat transactions and data size of storage")
	cfg := stat.StatConfig{}
	viper.MustUnmarshalKey("stat", &cfg)

	dataCtx := MustInitDataContext()
	defer dataCtx.Close()

	startTime := stat.MustDefaultRangeStart(dataCtx.Eth)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	stSubmit := stat.MustNewStatSubmit(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stSubmit.DoStat(ctx, &wg)
	stSyncStatus := stat.MustNewSyncStatusStat(dataCtx.DB, dataCtx.L2Sdk)
	go stSyncStatus.DoStat(ctx, &wg)
	stAddress := stat.MustNewStatAddress(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stAddress.DoStat(ctx, &wg)
	stMiner := stat.MustNewStatMiner(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stMiner.DoStat(ctx, &wg)
	stDASubmit := stat.MustNewStatDASubmit(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stDASubmit.DoStat(ctx, &wg)
	stDAClient := stat.MustNewStatDAClient(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stDAClient.DoStat(ctx, &wg)
	stDASigner := stat.MustNewStatDASigner(&cfg, dataCtx.DB, dataCtx.Eth, startTime)
	go stDASigner.DoStat(ctx, &wg)

	GracefulShutdown(&wg, cancel)
}
