package api

import (
	commonApi "github.com/Conflux-Chain/go-conflux-util/api"
)

var (
	ErrConfigNotFound        = commonApi.NewBusinessError(1001, "Config not found", nil)
	ErrAddressNotFound       = commonApi.NewBusinessError(1002, "Account not found", nil)
	ErrStatTypeNotSupported  = commonApi.NewBusinessError(1003, "Stat type not supported", nil)
	ErrStorageBaseFeeNotStat = commonApi.NewBusinessError(1004, "Storage base fee not stat", nil)
	ErrLayer1TxPruned        = commonApi.NewBusinessError(1005, "Layer1 transaction pruned", nil)
	ErrLayer1ReceiptPruned   = commonApi.NewBusinessError(1006, "Layer1 receipt pruned", nil)
)
