package api

import (
	"strings"
	"time"

	"github.com/0glabs/0g-storage-scan/stat"
	"github.com/shopspring/decimal"
)

type PageParam struct {
	Skip  int    `form:"skip,default=0" binding:"omitempty,gte=0"`
	Limit int    `form:"limit,default=10" binding:"omitempty,lte=2000"`
	Sort  string `form:"sort,default=desc" binding:"omitempty,oneof=asc desc"`
}

func (sp *PageParam) isDesc() bool {
	return strings.EqualFold(sp.Sort, "desc")
}

type statParam struct {
	PageParam
	MinTimestamp *int   `form:"minTimestamp" binding:"omitempty,number"`
	MaxTimestamp *int   `form:"maxTimestamp" binding:"omitempty,number"`
	IntervalType string `form:"intervalType,default=day" binding:"omitempty,oneof=hour day"`
}

// DataStatList model info
// @Description Storage data list
type DataStatList struct {
	Total int64      `json:"total"` // The total number of stat returned
	List  []DataStat `json:"list"`  // Stat list
}

// TxStatList model info
// @Description Storage transaction list
type TxStatList struct {
	Total int64    `json:"total"` // The total number of stat returned
	List  []TxStat `json:"list"`  // Stat list
}

// FeeStatList model info
// @Description Storage fee list
type FeeStatList struct {
	Total int64     `json:"total"` // The total number of stat returned
	List  []FeeStat `json:"list"`  // Stat list
}

// DataStat model info
// @Description Storage data information
type DataStat struct {
	StatTime  time.Time `json:"statTime"`  // Statistics time
	FileCount uint64    `json:"fileCount"` // Number of files in a specific time interval
	FileTotal uint64    `json:"fileTotal"` // Total number of files by a certain time
	DataSize  uint64    `json:"dataSize"`  // Size of storage data in a specific time interval
	DataTotal uint64    `json:"dataTotal"` // Total Size of storage data by a certain time
}

// TxStat model info
// @Description Storage transaction information
type TxStat struct {
	StatTime time.Time `json:"statTime"` // Statistics time
	TxCount  uint64    `json:"txCount"`  // Number of layer1 transaction in a specific time interval
	TxTotal  uint64    `json:"txTotal"`  // Total number of layer1 transaction by a certain time
}

// FeeStat model info
// @Description Storage fee information
type FeeStat struct {
	StatTime        time.Time       `json:"statTime"`        // Statistics time
	StorageFee      decimal.Decimal `json:"storageFee"`      // The base fee for storage in a specific time interval
	StorageFeeTotal decimal.Decimal `json:"storageFeeTotal"` // The total base fee for storage by a certain time
}

// Summary model info
// @Description Storage summary information
type Summary struct {
	StorageFeeStat   `json:"storageFee"` // Storage fee information
	stat.LogSyncInfo `json:"logSync"`    // Synchronization information of submit event
}

// StorageFeeStat model info
// @Description Stat storage fee information
type StorageFeeStat struct {
	TokenInfo       `json:"chargeToken"` // Charge token info
	StorageFeeTotal decimal.Decimal      `json:"storageFeeTotal"` // Total storage fee
}

// TokenInfo model info
// @Description Charge token information
type TokenInfo struct {
	Address  string `json:"address"`  // The address of the token contract
	Name     string `json:"name"`     // Token name
	Symbol   string `json:"symbol"`   // Token symbol
	Decimals uint8  `json:"decimals"` // Token decimals
	Native   bool   `json:"native"`   // True is native token, otherwise is not
}

type listAddressStorageTxParam struct {
	PageParam
	RootHash *string `form:"rootHash" binding:"omitempty"`
}

// StorageTxList model info
// @Description Submission information list
type StorageTxList struct {
	Total int64           `json:"total"` // The total number of submission returned
	List  []StorageTxInfo `json:"list"`  // Submission list
}

// StorageTxInfo model info
// @Description Submission transaction information
type StorageTxInfo struct {
	TxSeq  uint64 `json:"txSeq"`  // Submission index in submit event
	From   string `json:"from"`   // File uploader address
	Method string `json:"method"` // The name of the submit event is always `submit`

	RootHash   string          `json:"rootHash"`   // Merkle root of the file to upload
	DataSize   uint64          `json:"dataSize"`   // File size in bytes
	StorageFee decimal.Decimal `json:"storageFee"` // The storage fee required to upload the file
	Status     uint8           `json:"status"`     // File upload status, 0-not uploaded,1-uploading,2-uploaded

	BlockNumber uint64 `json:"blockNumber"` // The block where the submit event is emitted
	TxHash      string `json:"txHash"`      // The transaction where the submit event is emitted
	Timestamp   int64  `json:"timestamp"`   // The block time when submit event emits

	Segments         uint64 `json:"segments"`         // The total number of segments the file is split into
	UploadedSegments uint64 `json:"uploadedSegments"` // The number of segments the file has been uploaded
}

// StorageTxDetail model info
// @Description Submission transaction information
type StorageTxDetail struct {
	TxSeq  string `json:"txSeq"`  // Submission index in submit event
	From   string `json:"from"`   // File uploader address
	Method string `json:"method"` // The name of the submit event is always `submit`

	RootHash   string          `json:"rootHash"`   // Merkle root of the file to upload
	DataSize   uint64          `json:"dataSize"`   // File size in bytes
	Expiration uint64          `json:"expiration"` // Expiration date of the uploaded file
	StorageFee decimal.Decimal `json:"storageFee"` // The storage fee required to upload the file
	Status     uint8           `json:"status"`     // File upload status, 0-not uploaded,1-uploading,2-uploaded

	StartPosition    uint64 `json:"startPosition"`    // The starting position of the file stored in the storage node
	EndPosition      uint64 `json:"endPosition"`      // The ending position of the file stored in the storage node
	Segments         uint64 `json:"segments"`         // The total number of segments the file is split into
	UploadedSegments uint64 `json:"uploadedSegments"` // The number of segments the file has been uploaded

	BlockNumber uint64 `json:"blockNumber"` // The block where the submit event is emitted
	TxHash      string `json:"txHash"`      // The transaction where the submit event is emitted
	Timestamp   uint64 `json:"timestamp"`   // The block time when submit event emits

	GasFee   uint64 `json:"gasFee"`   // The gas fee of the transaction on layer1
	GasUsed  uint64 `json:"gasUsed"`  // The gas used of the transaction on layer1
	GasLimit uint64 `json:"gasLimit"` // The gas limit of the transaction on layer1
}

// RewardList model info
// @Description Miner reward list
type RewardList struct {
	Total int64    `json:"total"` // The total number of miner reward returned
	List  []Reward `json:"list"`  // Miner reward list
}

// Reward model info
// @Description Reward information
type Reward struct {
	Miner       string          `json:"miner"`       // Miner address
	Amount      decimal.Decimal `json:"amount"`      // The reward amount
	BlockNumber uint64          `json:"blockNumber"` // The block where the reward event is emitted
	TxHash      string          `json:"txHash"`      // The transaction where the reward event is emitted
	Timestamp   int64           `json:"timestamp"`   // The block time when reward event emits
}

type AddressInfo struct {
	address   string
	addressId uint64
}

type AccountInfo struct {
	Balance decimal.Decimal `json:"balance"` // The balance in layer 1

	FileCount  uint64          `json:"fileCount"`       // Total number of files
	TxCount    uint64          `json:"txCount"`         // Total number of layer1 transaction
	DataSize   uint64          `json:"dataTotal"`       // Total Size of storage data
	StorageFee decimal.Decimal `json:"storageFeeTotal"` // Total storage fee

	RewardCount uint64 `json:"rewardCount"` // Total number of distributed reward recodes
}
