package storage

import (
	"strings"
	"time"

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

type listStorageTxParam struct {
	PageParam
	RootHash     *string `form:"rootHash" binding:"omitempty"`
	TxHash       *string `form:"txHash" binding:"omitempty"`
	StorageClass *string `form:"storageClass" binding:"omitempty,oneof=hot standard"`
	MinTimestamp *int    `form:"minTimestamp" binding:"omitempty,number"`
	MaxTimestamp *int    `form:"maxTimestamp" binding:"omitempty,number"`
}

type listMinerParam struct {
	PageParam
	SortField string `form:"sortField,default=latest_update_time" binding:"omitempty,oneof=latest_update_time total_reward"`
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

// AddressStatList model info
// @Description Hex40 address stat list
type AddressStatList struct {
	Total int64         `json:"total"` // The total number of stat returned
	List  []AddressStat `json:"list"`  // Stat list
}

// MinerStatList model info
// @Description Miner stat list
type MinerStatList struct {
	Total int64       `json:"total"` // The total number of stat returned
	List  []MinerStat `json:"list"`  // Stat list
}

// RewardStatList model info
// @Description Miner reward stat list
type RewardStatList struct {
	Total int64        `json:"total"` // The total number of stat returned
	List  []RewardStat `json:"list"`  // Stat list
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

// AddressStat model info
// @Description Hex40 stat data information
type AddressStat struct {
	StatTime      time.Time `json:"statTime"`      // Statistics time
	AddressNew    uint64    `json:"addressNew"`    // Number of newly increased hex40 in a specific time interval
	AddressActive uint64    `json:"addressActive"` // Number of active hex40 in a specific time interval
	AddressTotal  uint64    `json:"addressTotal"`  // Total number of hex40 by a certain time
}

// MinerStat model info
// @Description Miner stat data information
type MinerStat struct {
	StatTime    time.Time `json:"statTime"`    // Statistics time
	MinerNew    uint64    `json:"minerNew"`    // Number of newly increased miner in a specific time interval
	MinerActive uint64    `json:"minerActive"` // Number of active miner in a specific time interval
	MinerTotal  uint64    `json:"minerTotal"`  // Total number of miner by a certain time
}

// RewardStat model info
// @Description Miner reward stat data information
type RewardStat struct {
	StatTime    time.Time       `json:"statTime"`    // Statistics time
	RewardNew   decimal.Decimal `json:"rewardNew"`   // Newly increased miner reward in a specific time interval
	RewardTotal decimal.Decimal `json:"rewardTotal"` // Total miner reward by a certain time
}

// LogSyncInfo model info
// @Description Submit log sync information
type LogSyncInfo struct {
	Layer1LogSyncHeight uint64 `json:"layer1-logSyncHeight"` // Synchronization height of submit log on blockchain
	LogSyncHeight       uint64 `json:"logSyncHeight"`        // Synchronization height of submit log on storage node
}

// Summary model info
// @Description Storage summary information
type Summary struct {
	StorageFeeStat  `json:"storageFee"`  // Storage fee information
	LogSyncInfo     `json:"logSync"`     // Synchronization information of submit event
	StorageFileStat `json:"storageFile"` // Storage file information
	MinerRewardStat `json:"minerReward"` // Miner reward information
}

// StorageFeeStat model info
// @Description Stat storage fee information
type StorageFeeStat struct {
	TokenInfo       `json:"chargeToken"` // Charge token info
	StorageFeeTotal decimal.Decimal      `json:"storageFeeTotal"` // Total storage fee
}

// StorageFileStat model info
// @Description Stat storage file information
type StorageFileStat struct {
	TotalExpiredFiles uint64 `json:"totalExpiredFiles"` // Total number of expired files
	TotalPrunedFiles  uint64 `json:"totalPrunedFiles"`  // Total number of pruned files
}

// MinerRewardStat model info
// @Description Stat miner reward information
type MinerRewardStat struct {
	AvgReward24Hours decimal.Decimal `json:"avgReward24Hours"` // average reward in 24 hours
	TotalReward      decimal.Decimal `json:"totalReward"`      // Total amount of miner reward
	TotalWinCount    uint64          `json:"totalWinCount"`    // Total win count
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

// StorageTxList model info
// @Description Submission information list
type StorageTxList struct {
	Total int64            `json:"total"` // The total number of submission returned
	List  []*StorageTxInfo `json:"list"`  // Submission list
}

// StorageTxInfo model info
// @Description Submission transaction information
type StorageTxInfo struct {
	TxSeq  uint64 `json:"txSeq"` // Submission index in submit event
	From   string `json:"from"`  // File uploader address
	FromId uint64 `json:"-"`
	Method string `json:"method"` // The name of the submit event is always `submit`

	RootHash   string          `json:"rootHash"`   // Merkle root of the file to upload
	DataSize   uint64          `json:"dataSize"`   // File size in bytes
	Expiration uint64          `json:"expiration"` // Expiration timestamp of the uploaded file
	StorageFee decimal.Decimal `json:"storageFee"` // The storage fee required to upload the file
	Status     uint8           `json:"status"`     // File upload status, 0-not uploaded,1-partial uploaded,2-uploaded,3-pruned

	BlockNumber uint64 `json:"blockNumber"` // The block where the submit event is emitted
	TxHash      string `json:"txHash"`      // The transaction where the submit event is emitted
	Timestamp   int64  `json:"timestamp"`   // The block time when submit event emits

	Segments         uint64 `json:"segments"`         // The total number of segments the file is split into
	UploadedSegments uint64 `json:"uploadedSegments"` // The number of segments the file has been uploaded

	StorageClass string `json:"storageClass"` // Storage class: "hot" (cached on a hot-storage provider) or "standard"
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
	Status     uint8           `json:"status"`     // File upload status, 0-not uploaded,1-partial uploaded,2-uploaded,3-pruned

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

	StorageClass string `json:"storageClass"` // Storage class: "hot" (cached on a hot-storage provider) or "standard"
}

// AccountStats model info
// @Description Account file statistics
type AccountStats struct {
	TotalFiles        int64           `json:"totalFiles"`        // Total number of files uploaded
	TotalBytes        uint64          `json:"totalBytes"`        // Total size of uploaded files in bytes
	TotalStorageFee   decimal.Decimal `json:"totalStorageFee"`   // Total storage fee paid
	ExpiredFiles      int64           `json:"expiredFiles"`      // Number of files past their expiration
	ExpiringSoonFiles int64           `json:"expiringSoonFiles"` // Number of files expiring within 7 days
	HealthyFiles      int64           `json:"healthyFiles"`      // Number of files with more than 7 days remaining
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
	Amount      decimal.Decimal `json:"reward"`      // The reward amount
	BlockNumber uint64          `json:"blockNumber"` // The block where the reward event is emitted
	TxHash      string          `json:"txHash"`      // The transaction where the reward event is emitted
	Timestamp   int64           `json:"timestamp"`   // The block time when reward event emits
}

// MinerList model info
// @Description Miner  list
type MinerList struct {
	Total int64   `json:"total"` // The total number of miner returned
	List  []Miner `json:"list"`  // Miner list
}

// Miner model info
// @Description Miner information
type Miner struct {
	Miner          string          `json:"miner"`          // Miner address
	TotalReward    decimal.Decimal `json:"totalReward"`    // The total reward amount
	WinCount       uint64          `json:"winCount"`       // Win count
	MiningAttempts uint64          `json:"miningAttempts"` // Mining attempts
	Timestamp      int64           `json:"timestamp"`      // The block time when the latest reward event emits
}

type AddressInfo struct {
	address   string
	addressId uint64
}

type AccountInfo struct {
	Balance decimal.Decimal `json:"balance"` // The balance in layer 1

	DataSize     uint64          `json:"dataSize"`     // Total Size of storage data
	StorageFee   decimal.Decimal `json:"storageFee"`   // Total storage fee
	Txs          uint64          `json:"txs"`          // Total number of layer1 transaction
	Files        uint64          `json:"files"`        // Total number of files
	ExpiredFiles uint64          `json:"expiredFiles"` // The number of expired files
	PrunedFiles  uint64          `json:"prunedFiles"`  // The number of pruned files

	TotalReward decimal.Decimal `json:"totalReward"` // Total amount of distributed rewards
}

type MinerInfo struct {
	Balance     decimal.Decimal `json:"balance"`     // The balance in layer 1
	TotalReward decimal.Decimal `json:"totalReward"` // Total amount of distributed rewards
}

type topnParam struct {
	SpanType string `form:"spanType" binding:"omitempty,oneof=24h 3d 7d"`
}

type Topn struct {
	Rank    int    `json:"rank"`    // Data ranking
	Address string `json:"address"` // Address on blockchain
}

// DataTopn model info
// @Description Storage data size topn information
type DataTopn struct {
	Topn
	DataSize uint64 `json:"dataSize"` // Size of storage data

}

// FeeTopn model info
// @Description Storage fee topn information
type FeeTopn struct {
	Topn
	StorageFee decimal.Decimal `json:"storageFee"` // The total base fee for storage
}

// TxsTopn model info
// @Description Storage transaction topn information
type TxsTopn struct {
	Topn
	Txs uint64 `json:"txs"` // Number of layer1 transaction
}

// FilesTopn model info
// @Description Storage files topn information
type FilesTopn struct {
	Topn
	Files uint64 `json:"files"` // Number of files
}

// RewardTopn model info
// @Description Reward topn information
type RewardTopn struct {
	Rank           int             `json:"rank"`           // Data ranking
	Address        string          `json:"miner"`          // Address on blockchain
	Amount         decimal.Decimal `json:"totalReward"`    // Reward amount
	WinCount       uint64          `json:"winCount"`       // Win count
	MiningAttempts uint64          `json:"miningAttempts"` // Mining attempts
}

// DataTopnList model info
// @Description Topn list of data size
type DataTopnList struct {
	List []DataTopn `json:"list"` // Topn list
}

// FeeTopnList model info
// @Description Topn list of storage fee
type FeeTopnList struct {
	List []FeeTopn `json:"list"` // Topn list
}

// TxsTopnList model info
// @Description Topn list of layer1 transactions
type TxsTopnList struct {
	List []TxsTopn `json:"list"` // Topn list
}

// FilesTopnList model info
// @Description Topn list of files
type FilesTopnList struct {
	List []FilesTopn `json:"list"` // Topn list
}

// RewardTopnList model info
// @Description Topn list of files
type RewardTopnList struct {
	List []RewardTopn `json:"list"` // Topn list
}
