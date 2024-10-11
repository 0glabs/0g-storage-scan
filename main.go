package main

import (
	"github.com/0glabs/0g-storage-scan/cmd"
	_ "github.com/0glabs/0g-storage-scan/docs"
	"github.com/Conflux-Chain/go-conflux-util/config"
)

func main() {
	config.MustInit("zg_scan")
	cmd.Execute()
}
