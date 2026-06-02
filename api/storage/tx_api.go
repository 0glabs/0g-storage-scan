package storage

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/0glabs/0g-storage-client/core"
	scanApi "github.com/0glabs/0g-storage-scan/api"
	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func listStorageTxs(c *gin.Context) (interface{}, error) {
	var param listStorageTxParam
	if err := c.ShouldBind(&param); err != nil {
		return nil, api.ErrValidation(errors.WithMessage(err, "Invalid list storage submits param"))
	}

	total, submits, err := listSubmits(nil, param)
	if err != nil {
		return nil, err
	}

	submits, err = refreshFileInfos(submits)
	if err != nil {
		return nil, err
	}

	return convertStorageTxs(total, submits)
}

func getStorageTx(c *gin.Context) (interface{}, error) {
	txSeqParam := c.Param("txSeq")
	txSeq, err := strconv.ParseUint(txSeqParam, 10, 64)
	if err != nil {
		return nil, api.ErrValidation(errors.WithMessagef(err, "Invalid txSeq '%v'", txSeqParam))
	}

	var submit store.Submit
	exist, err := db.Store.Exists(&submit, "submission_index = ?", txSeq)
	if err != nil {
		return nil, scanApi.ErrDatabase(errors.WithMessage(err, "Failed to get submit list"))
	}
	if !exist {
		return nil, scanApi.ErrNoMatchingRecordFound(errors.Errorf("submit(txSeq %v)", txSeq))
	}

	addrIDs := []uint64{submit.SenderID}
	addrMap, err := db.BatchGetAddresses(addrIDs)
	if err != nil {
		return nil, scanApi.ErrDatabase(err)
	}

	submits, err := refreshFileInfos([]store.Submit{submit})
	if err != nil {
		return nil, err
	}
	submit = submits[0]

	if submit.Status == uint8(rpc.PrunedCounted) {
		submit.Status = uint8(rpc.Pruned)
	}

	submitTime := uint64(submit.BlockTime.Unix())
	result := StorageTxDetail{
		TxSeq:       strconv.FormatUint(submit.SubmissionIndex, 10),
		From:        addrMap[submit.SenderID].Address,
		Method:      "submit",
		RootHash:    submit.RootHash,
		DataSize:    submit.Length,
		Status:      submit.Status,
		StorageFee:  submit.Fee,
		BlockNumber: submit.BlockNumber,
		TxHash:      submit.TxHash,
		Timestamp:   submitTime,
		Expiration:  submitTime + expireSeconds.Uint64(),

		StorageClass: normalizeStorageClass(submit.StorageClass),
	}

	var extra store.SubmitExtra
	if err := json.Unmarshal(submit.Extra, &extra); err != nil {
		return nil, scanApi.ErrUnmarshalValue(errors.New("submit extra"))
	}

	result.StartPosition = extra.StartPos.Uint64()
	trunksWithoutPadding := (submit.Length-1)/core.DefaultChunkSize + 1
	result.EndPosition = extra.StartPos.Uint64() + trunksWithoutPadding
	result.Segments = submit.TotalSegNum
	result.UploadedSegments = submit.UploadedSegNum

	if extra.GasPrice > 0 {
		result.GasFee = extra.GasPrice * extra.GasUsed
		result.GasUsed = extra.GasUsed
		result.GasLimit = extra.GasLimit
		return result, nil
	}

	hash := common.HexToHash(submit.TxHash)
	tx, err := sdk.Eth.TransactionByHash(hash)
	if err != nil {
		return nil, scanApi.ErrBlockchainRPC(errors.WithMessagef(err, "Failed to get transaction, txSeq %v", txSeq))
	}
	if tx == nil {
		return nil, scanApi.ErrBlockchainRPC(errors.Errorf("Transaction pruned"))
	}
	rcpt, err := sdk.Eth.TransactionReceipt(hash)
	if err != nil {
		return nil, scanApi.ErrBlockchainRPC(errors.WithMessagef(err, "Failed to get receipt, txSeq %v", txSeq))
	}
	if rcpt == nil {
		return nil, scanApi.ErrBlockchainRPC(errors.Errorf("Receipt pruned"))
	}
	result.GasFee = tx.GasPrice.Uint64() * rcpt.GasUsed
	result.GasUsed = rcpt.GasUsed
	result.GasLimit = tx.Gas
	return result, nil
}

func listAddressStorageTxs(c *gin.Context) (interface{}, error) {
	addressInfo, err := getAddressInfo(c)
	if err != nil {
		return nil, err
	}
	addrIDPtr := &addressInfo.ID

	var param listStorageTxParam
	if err := c.ShouldBind(&param); err != nil {
		return nil, api.ErrValidation(errors.Errorf("Invalid list address's storage submits param"))
	}

	total, submits, err := listSubmits(addrIDPtr, param)
	if err != nil {
		return nil, err
	}
	if len(submits) == 0 {
		return &StorageTxList{
			Total: total,
			List:  make([]*StorageTxInfo, 0),
		}, nil
	}

	submits, err = refreshFileInfos(submits)
	if err != nil {
		return nil, err
	}

	return convertStorageTxs(total, submits)
}

func listSubmits(addressID *uint64, params listStorageTxParam) (int64,
	[]store.Submit, error) {
	if addressID == nil {
		total, submits, err := db.SubmitStore.List(params.RootHash, params.TxHash, params.StorageClass,
			params.isDesc(), params.Skip, params.Limit)
		if err != nil {
			return 0, nil, scanApi.ErrDatabase(errors.WithMessage(err, "Failed to get submit list"))
		}
		return total, submits, nil
	}

	total, addrSubmits, err := db.AddressSubmitStore.List(addressID, params.RootHash, params.TxHash,
		params.StorageClass, params.MinTimestamp, params.MaxTimestamp, params.isDesc(), params.Skip, params.Limit)
	if err != nil {
		return 0, nil, scanApi.ErrDatabase(errors.WithMessage(err, "Failed to get account's submit list"))
	}

	submits := make([]store.Submit, 0)
	for _, as := range addrSubmits {
		submits = append(submits, store.Submit{
			SubmissionIndex: as.SubmissionIndex,
			RootHash:        as.RootHash,
			SenderID:        as.SenderID,
			Length:          as.Length,
			BlockNumber:     as.BlockNumber,
			BlockTime:       as.BlockTime,
			TxHash:          as.TxHash,
			Status:          as.Status,
			TotalSegNum:     as.TotalSegNum,
			UploadedSegNum:  as.UploadedSegNum,
			Fee:             as.Fee,
			StorageClass:    as.StorageClass,
		})
	}

	return total, submits, nil
}

func convertStorageTxs(total int64, submits []store.Submit) (*StorageTxList, error) {
	addrIDs := make([]uint64, 0)
	for _, submit := range submits {
		addrIDs = append(addrIDs, submit.SenderID)
	}
	addrMap, err := db.BatchGetAddresses(addrIDs)
	if err != nil {
		return nil, scanApi.ErrDatabase(err)
	}

	storageTxs := make([]*StorageTxInfo, 0)
	for _, submit := range submits {
		if submit.Status == uint8(rpc.PrunedCounted) {
			submit.Status = uint8(rpc.Pruned)
		}
		submitTime := uint64(submit.BlockTime.Unix())
		storageTx := &StorageTxInfo{
			TxSeq:            submit.SubmissionIndex,
			BlockNumber:      submit.BlockNumber,
			TxHash:           submit.TxHash,
			RootHash:         submit.RootHash,
			From:             addrMap[submit.SenderID].Address,
			FromId:           submit.SenderID,
			Method:           "submit",
			Status:           submit.Status,
			Segments:         submit.TotalSegNum,
			UploadedSegments: submit.UploadedSegNum,
			Timestamp:        submit.BlockTime.Unix(),
			Expiration:       submitTime + expireSeconds.Uint64(),
			DataSize:         submit.Length,
			StorageFee:       submit.Fee,
			StorageClass:     normalizeStorageClass(submit.StorageClass),
		}
		storageTxs = append(storageTxs, storageTx)
	}

	return &StorageTxList{
		Total: total,
		List:  storageTxs,
	}, nil
}

func refreshFileInfos(submits []store.Submit) ([]store.Submit, error) {
	unfinalizedSubmits := make([]store.Submit, 0)
	for _, submit := range submits {
		if submit.Status < uint8(rpc.Pruned) {
			unfinalizedSubmits = append(unfinalizedSubmits, submit)
		}
	}

	if len(unfinalizedSubmits) == 0 {
		return submits, nil
	}

	result, err := db.UpdateFileInfos(context.Background(), unfinalizedSubmits, storageConfig)
	if err != nil {
		return nil, err
	}

	for _, submit := range submits {
		fileInfo := result[submit.SubmissionIndex]
		if fileInfo != nil && fileInfo.Err == nil {
			submit.Status = fileInfo.Data.Status
			submit.UploadedSegNum = fileInfo.Data.UploadedSegNum
		}
	}

	return submits, nil
}

// normalizeStorageClass returns the storage class for API responses, defaulting
// to "standard" when unset (e.g. legacy rows the reconcile worker has not yet
// classified).
func normalizeStorageClass(class string) string {
	if class == "" {
		return store.StorageClassStandard
	}
	return class
}
