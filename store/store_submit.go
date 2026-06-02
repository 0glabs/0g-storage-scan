package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/0glabs/0g-storage-client/contract"
	"github.com/0glabs/0g-storage-client/core"
	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/ethereum/go-ethereum/common"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	submitPartitionSize = 10_000_000
)

// Storage class values for the storage_class column. A file is "hot" when it is
// currently cached on a hot-storage provider (per the hot router), "standard"
// otherwise (the default — it lives only on the base storage network).
const (
	StorageClassStandard = "standard"
	StorageClassHot      = "hot"
)

type Submit struct {
	SubmissionIndex uint64 `gorm:"primaryKey;autoIncrement:false"`
	RootHash        string `gorm:"size:66;index:idx_root"`
	Sender          string `gorm:"-"`
	SenderID        uint64 `gorm:"not null"`
	Length          uint64 `gorm:"not null"`

	BlockNumber uint64    `gorm:"not null;index:idx_bn"`
	BlockTime   time.Time `gorm:"not null;index:idx_bt,sort:desc"`
	TxHash      string    `gorm:"size:66;not null;index:idx_txHash,length:10"`

	TotalSegNum    uint64          `gorm:"not null;default:0"`
	UploadedSegNum uint64          `gorm:"not null;default:0"`
	Status         uint8           `gorm:"not null;default:0"`
	Fee            decimal.Decimal `gorm:"type:decimal(65);not null"`

	// StorageClass is "hot" if the file is currently cached on a hot-storage
	// provider, "standard" otherwise. Defaults to "standard" at insert time and is
	// maintained by the storage-class reconcile worker.
	StorageClass string `gorm:"size:20;not null;default:standard;index"`

	Extra []byte `gorm:"type:mediumText"` // json field
}

type SubmitExtra struct {
	Identity   common.Hash         `json:"identity"`
	StartPos   *big.Int            `json:"startPos"`
	Submission contract.Submission `json:"submission"`
	GasPrice   uint64              `json:"gasPrice"`
	GasLimit   uint64              `json:"gasLimit"`
	GasUsed    uint64              `json:"gasUsed"`
}

func NewSubmit(pricePerSector *big.Int, blockTime time.Time, log types.Log, filter *contract.FlowFilterer) (*Submit,
	error) {
	flowSubmit, err := filter.ParseSubmit(*log.ToEthLog())
	if err != nil {
		return nil, err
	}

	extra, err := json.Marshal(SubmitExtra{
		Identity:   flowSubmit.Identity,
		StartPos:   flowSubmit.StartPos,
		Submission: flowSubmit.Submission,
	})
	if err != nil {
		return nil, err
	}

	length := flowSubmit.Submission.Length.Uint64()
	submit := &Submit{
		SubmissionIndex: flowSubmit.SubmissionIndex.Uint64(),
		RootHash:        flowSubmit.Submission.Root().String(),
		Sender:          flowSubmit.Sender.String(),
		Length:          length,
		BlockNumber:     log.BlockNumber,
		BlockTime:       blockTime,
		TxHash:          log.TxHash.String(),
		Fee:             decimal.NewFromBigInt(flowSubmit.Submission.Fee(pricePerSector), 0),
		TotalSegNum:     (length-1)/core.DefaultSegmentSize + 1,
		Extra:           extra,
	}

	return submit, nil
}

func (Submit) TableName() string {
	return "submits"
}

type SubmitStore struct {
	*mysql.Store

	partitioner *mysqlRangePartitioner
}

func newSubmitStore(db *gorm.DB, config mysql.Config) *SubmitStore {
	return &SubmitStore{
		Store: mysql.NewStore(db),
		partitioner: newMysqlRangePartitioner(
			config.Database, Submit{}.TableName(), "submission_index",
		),
	}
}

func (ss *SubmitStore) preparePartition(dataSlice []Submit) error {
	partition, err := ss.partitioner.latestPartition(ss.DB)
	if err != nil {
		return errors.WithMessage(err, "Failed to get latest partition")
	}

	var latestPartitionIndex int
	if partition != nil {
		latestPartitionIndex = ss.partitioner.indexOfPartition(partition)
	} else {
		// create initial partition
		initPartitionIndex := int(dataSlice[0].SubmissionIndex / submitPartitionSize)
		threshold := uint64(initPartitionIndex+1) * submitPartitionSize
		err = ss.partitioner.convert(ss.DB, initPartitionIndex, threshold)
		if err != nil {
			return errors.WithMessage(err, "Failed to init range partitioned table")
		}
		latestPartitionIndex = initPartitionIndex
	}

	for _, data := range dataSlice {
		if data.SubmissionIndex%submitPartitionSize != 0 {
			continue
		}

		// create new partition if necessary
		partitionIndex := int(data.SubmissionIndex / submitPartitionSize)
		if partitionIndex <= latestPartitionIndex { // partition already exists
			continue
		}

		threshold := uint64(partitionIndex+1) * submitPartitionSize
		err := ss.partitioner.addPartition(ss.DB, partitionIndex, threshold)
		if err != nil {
			return errors.WithMessage(err, "Failed to add partition")
		}

		latestPartitionIndex = partitionIndex
	}

	return nil
}

func (ss *SubmitStore) Add(dbTx *gorm.DB, submits []Submit) error {
	return dbTx.CreateInBatches(submits, batchSizeInsert).Error
}

func (ss *SubmitStore) Pop(dbTx *gorm.DB, block uint64) error {
	return dbTx.Where("block_number >= ?", block).Delete(&Submit{}).Error
}

func (ss *SubmitStore) Count(startTime, endTime time.Time) (*SubmitStatResult, error) {
	var result SubmitStatResult
	err := ss.DB.Model(&Submit{}).Select(`count(*) as file_count, 
		IFNULL(sum(length), 0) as data_size, IFNULL(sum(fee), 0) as base_fee, count(distinct tx_hash) as tx_count,
		count(distinct sender_id) as sender_count`).
		Where("block_time >= ? and block_time < ?", startTime, endTime).Find(&result).Error
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (ss *SubmitStore) UpdateByPrimaryKey(dbTx *gorm.DB, s *Submit) error {
	db := ss.DB
	if dbTx != nil {
		db = dbTx
	}

	if err := db.Model(&s).Where("submission_index=?", s.SubmissionIndex).
		Updates(s).Error; err != nil {
		return err
	}

	return nil
}

func (ss *SubmitStore) UpdateByPrimaryKeys(dbTx *gorm.DB, s *Submit, submissionIndexes []uint64) error {
	db := ss.DB
	if dbTx != nil {
		db = dbTx
	}

	if err := db.Model(&s).Where("submission_index in ?", submissionIndexes).
		Updates(s).Error; err != nil {
		return err
	}

	return nil
}

// SetStorageClassByRootHashes sets storage_class=class for the submits whose
// root hash is in rootHashes and whose class currently differs. The "currently
// differs" guard keeps steady-state reconcile cycles near-zero-write. Returns
// the number of rows updated.
func (ss *SubmitStore) SetStorageClassByRootHashes(class string, rootHashes []string) (int64, error) {
	if len(rootHashes) == 0 {
		return 0, nil
	}
	res := ss.DB.Model(&Submit{}).
		Where("root_hash IN ?", rootHashes).
		Where("storage_class <> ?", class).
		Update("storage_class", class)
	return res.RowsAffected, res.Error
}

// ListHotRootHashes returns the root hashes of all submits currently marked hot.
// Used by the reconcile worker to find files that were evicted from the hot tier
// (currently hot in the DB but absent from the router's hot set) so they can be
// demoted back to standard.
func (ss *SubmitStore) ListHotRootHashes() ([]string, error) {
	var rootHashes []string
	err := ss.DB.Model(&Submit{}).
		Where("storage_class = ?", StorageClassHot).
		Pluck("root_hash", &rootHashes).Error
	return rootHashes, err
}

func (ss *SubmitStore) List(rootHash *string, txHash *string, storageClass *string, idDesc bool, skip, limit int) (int64, []Submit, error) {
	dbRaw := ss.DB.Model(&Submit{})
	var conds []func(db *gorm.DB) *gorm.DB
	if rootHash != nil {
		conds = append(conds, RootHash(*rootHash))
	}
	if txHash != nil {
		conds = append(conds, TxHash(*txHash))
	}
	if storageClass != nil {
		conds = append(conds, StorageClass(*storageClass))
	}
	dbRaw.Scopes(conds...)

	var orderBy string
	if idDesc {
		orderBy = "submission_index DESC"
	} else {
		orderBy = "submission_index ASC"
	}

	list := new([]Submit)

	if len(conds) == 0 {
		var maxId sql.NullInt64
		if err := ss.DB.Model(&Submit{}).Select("MAX(submission_index)").Find(&maxId).Error; err != nil {
			return 0, nil, err
		}
		if !maxId.Valid {
			return 0, nil, nil
		}

		if skip > 0 {
			if idDesc {
				dbRaw.Where("submission_index <= ?", maxId.Int64-int64(skip))
			} else {
				dbRaw.Where("submission_index > ?", skip)
			}
		}
		if err := dbRaw.Order(orderBy).Limit(limit).Find(list).Error; err != nil {
			return 0, nil, err
		}

		return maxId.Int64, *list, nil
	}

	total, err := ss.Store.ListByOrder(dbRaw, orderBy, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}

func (ss *SubmitStore) QueryDesc(batch int) (
	[]Submit, error) {
	return ss.query(nil, nil, nil, true, batch)
}

func (ss *SubmitStore) QueryAscWithCursor(minSubmissionIndex *uint64, batch int) (
	[]Submit, error) {
	return ss.query(minSubmissionIndex, nil, nil, false, batch)
}

func (ss *SubmitStore) QueryFinalizedAscWithCursor(minSubmissionIndex, maxSubmissionIndex *uint64) (
	[]Submit, error) {
	batch := int(*maxSubmissionIndex - *minSubmissionIndex + 1)
	return ss.query(minSubmissionIndex, maxSubmissionIndex, []rpc.Status{rpc.Pruned}, false, batch)
}

func (ss *SubmitStore) query(minSubmissionIndex, maxSubmissionIndex *uint64, status []rpc.Status, isDesc bool, batch int) (
	[]Submit, error) {
	db := ss.DB.Model(&Submit{}).Select("submission_index, sender_id, total_seg_num, tx_hash, extra")

	if minSubmissionIndex != nil && maxSubmissionIndex != nil {
		db = db.Where("submission_index between ? and ?", minSubmissionIndex, maxSubmissionIndex)
	}
	if minSubmissionIndex != nil && maxSubmissionIndex == nil {
		db = db.Where("submission_index >= ?", minSubmissionIndex)
	}
	if minSubmissionIndex == nil && maxSubmissionIndex != nil {
		db = db.Where("submission_index <= ?", maxSubmissionIndex)
	}
	if len(status) > 0 {
		db = db.Where("status in ?", status)
	}

	if isDesc {
		db = db.Order("submission_index desc")
	} else {
		db = db.Order("submission_index asc")
	}

	db = db.Limit(batch)

	submits := new([]Submit)
	if err := db.Scan(submits).Error; err != nil {
		return nil, err
	}

	return *submits, nil
}

func (ss *SubmitStore) MaxSubmissionIndex() (uint64, error) {
	var maxId sql.NullInt64

	if err := ss.DB.Model(&Submit{}).Select("MAX(submission_index)").Find(&maxId).Error; err != nil {
		return 0, err
	}

	if !maxId.Valid {
		return 0, errors.New("Invalid submission index")
	}

	return uint64(maxId.Int64), nil
}

func (ss *SubmitStore) MaxSubmissionIndexFinalized(finalizedBN uint64) (uint64, bool, error) {
	var submit Submit

	result := ss.DB.Where("block_number <= ?", finalizedBN).Order("block_number desc").Limit(1).Find(&submit)
	if result.Error != nil {
		return 0, false, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, false, nil
	}

	return submit.SubmissionIndex, true, nil
}

func (ss *SubmitStore) MaxSubmissionIndexExpired(maxExpireTime time.Time) (uint64, bool, error) {
	var submit Submit

	result := ss.DB.Where("block_time <= ?", maxExpireTime).Order("block_time desc").Limit(1).Find(&submit)
	if result.Error != nil {
		return 0, false, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, false, nil
	}

	return submit.SubmissionIndex, true, nil
}

type GroupedSubmit struct {
	SenderID   uint64
	DataSize   uint64
	StorageFee decimal.Decimal
	Files      uint64
	Txs        uint64
	UpdatedAt  time.Time
}

func (ss *SubmitStore) GroupBySender(minSubmissionIndex, maxSubmissionIndex uint64) ([]GroupedSubmit, error) {
	groupedSubmits := new([]GroupedSubmit)
	err := ss.DB.Model(&Submit{}).
		Select(`sender_id, IFNULL(sum(length), 0) data_size, IFNULL(sum(fee), 0) storage_fee, count(*) files, 
		count(distinct tx_hash) txs, max(block_time) updated_at`).
		Where("submission_index between ? and ?", minSubmissionIndex, maxSubmissionIndex).
		Group("sender_id").
		Scan(groupedSubmits).Error

	if err != nil {
		return nil, err
	}

	return *groupedSubmits, nil
}

func (ss *SubmitStore) GroupBySenderByTime(startBlockTime, endBlockTime time.Time) ([]GroupedSubmit, error) {
	groupedSubmits := new([]GroupedSubmit)
	err := ss.DB.Model(&Submit{}).
		Select(`sender_id, IFNULL(sum(length), 0) data_size, IFNULL(sum(fee), 0) storage_fee, count(*) files, 
		count(distinct tx_hash) txs, max(block_time) updated_at`).
		Where("block_time >= ? and block_time < ?", startBlockTime, endBlockTime).
		Group("sender_id").
		Scan(groupedSubmits).Error

	if err != nil {
		return nil, err
	}

	return *groupedSubmits, nil
}

const (
	Min    = "1m"
	TenMin = "10m"
	Hour   = "1h"
	Day    = "1d"
)

var (
	Intervals = map[string]time.Duration{
		Min:    time.Minute,
		TenMin: time.Minute * 10,
		Hour:   time.Hour,
		Day:    time.Hour * 24,
	}

	IntervalTypes = map[string]string{
		"min":   Min,
		"10min": TenMin,
		"hour":  Hour,
		"day":   Day,
	}
)

type SubmitStat struct {
	ID           uint64          `json:"-"`
	StatType     string          `gorm:"size:4;not null;uniqueIndex:idx_statType_statTime,priority:1" json:"-"`
	StatTime     time.Time       `gorm:"not null;uniqueIndex:idx_statType_statTime,priority:2" json:"statTime"`
	FileCount    uint64          `gorm:"not null;default:0" json:"fileCount"`                     // Number of files in a specific time interval
	FileTotal    uint64          `gorm:"not null;default:0" json:"fileTotal"`                     // Total number of files by a certain time
	DataSize     uint64          `gorm:"not null;default:0" json:"dataSize"`                      // Size of storage data in a specific time interval
	DataTotal    uint64          `gorm:"not null;default:0" json:"dataTotal"`                     // Total Size of storage data by a certain time
	BaseFee      decimal.Decimal `gorm:"type:decimal(65);not null;default:0" json:"baseFee"`      // The base fee for storage
	BaseFeeTotal decimal.Decimal `gorm:"type:decimal(65);not null;default:0" json:"baseFeeTotal"` // The total base fee for storage
	TxCount      uint64          `gorm:"not null;default:0" json:"txCount"`                       // Number of layer1 transaction in a specific time interval
	TxTotal      uint64          `gorm:"not null;default:0" json:"txTotal"`                       // Total number of layer1 transaction by a certain time
}

func (SubmitStat) TableName() string {
	return "submit_stats"
}

type SubmitStatStore struct {
	*mysql.Store
}

func newSubmitStatStore(db *gorm.DB) *SubmitStatStore {
	return &SubmitStatStore{
		Store: mysql.NewStore(db),
	}
}

func (t *SubmitStatStore) LastByType(statType string) (*SubmitStat, error) {
	var submitStat SubmitStat
	err := t.Store.DB.Where("stat_type = ?", statType).Order("stat_time desc").Last(&submitStat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &submitStat, nil
}

type SubmitStatResult struct {
	FileCount   uint64
	DataSize    uint64
	BaseFee     decimal.Decimal
	TxCount     uint64
	SenderCount uint64
}

func (t *SubmitStatStore) Sum(startTime, endTime time.Time, statType string) (*SubmitStatResult, error) {
	nilTime := time.Time{}
	if startTime == nilTime && endTime == nilTime {
		return nil, errors.New("At least provide one parameter for startTime and endTime")
	}

	db := t.DB.Model(&SubmitStat{}).Select(`IFNULL(sum(file_count), 0) as file_count, 
		IFNULL(sum(data_size), 0) as data_size, IFNULL(sum(base_fee), 0) as base_fee, 
		IFNULL(sum(tx_count), 0) as tx_count`)
	db = db.Where("stat_type = ?", statType)
	if startTime != nilTime {
		db = db.Where("stat_time >= ?", startTime)
	}
	if endTime != nilTime {
		db = db.Where("stat_time < ?", endTime)
	}

	var sum SubmitStatResult
	err := db.Find(&sum).Error
	if err != nil {
		return nil, err
	}

	return &sum, nil
}

func (t *SubmitStatStore) Add(dbTx *gorm.DB, submitStat []*SubmitStat) error {
	return dbTx.CreateInBatches(submitStat, batchSizeInsert).Error
}

func (t *SubmitStatStore) Del(dbTx *gorm.DB, submitStat *SubmitStat) error {
	return dbTx.Where("stat_type = ? and stat_time = ?", submitStat.StatType, submitStat.StatTime).Delete(&SubmitStat{}).Error
}

func (t *SubmitStatStore) List(intervalType *string, minTimestamp, maxTimestamp *int, desc bool, skip, limit int) (int64,
	[]SubmitStat, error) {
	var conds []func(db *gorm.DB) *gorm.DB

	if intervalType != nil {
		intervalType := IntervalTypes[*intervalType]
		conds = append(conds, StatType(intervalType))
	}

	if minTimestamp != nil {
		conds = append(conds, MinTimestamp(*minTimestamp))
	}

	if maxTimestamp != nil {
		conds = append(conds, MaxTimestamp(*maxTimestamp))
	}

	dbRaw := t.DB.Model(&SubmitStat{})
	dbRaw.Scopes(conds...)

	list := new([]SubmitStat)
	total, err := t.Store.List(dbRaw, desc, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}

type SubmitTopnStat struct {
	ID         uint64
	StatTime   time.Time       `gorm:"not null;uniqueIndex:idx_statTime_addressId,priority:1"`
	AddressID  uint64          `gorm:"not null;uniqueIndex:idx_statTime_addressId,priority:2"`
	DataSize   uint64          `gorm:"not null;default:0"`                  // Size of storage data in a specific time interval
	StorageFee decimal.Decimal `gorm:"type:decimal(65);not null;default:0"` // The base fee for storage
	Txs        uint64          `gorm:"not null;default:0"`                  // Number of layer1 transaction in a specific time interval
	Files      uint64          `gorm:"not null;default:0"`                  // Number of files/layer2 transaction in a specific time interval
}

func (SubmitTopnStat) TableName() string {
	return "submit_topn_stats"
}

type SubmitTopnStatStore struct {
	*mysql.Store
}

func newSubmitTopnStatStore(db *gorm.DB) *SubmitTopnStatStore {
	return &SubmitTopnStatStore{
		Store: mysql.NewStore(db),
	}
}

func (t *SubmitTopnStatStore) BatchDeltaUpsert(dbTx *gorm.DB, submits []SubmitTopnStat) error {
	db := t.DB
	if dbTx != nil {
		db = dbTx
	}

	var placeholders string
	var params []interface{}
	size := len(submits)
	for i, s := range submits {
		placeholders += "(?,?,?,?,?,?)"
		if i != size-1 {
			placeholders += ",\n\t\t\t"
		}
		params = append(params, []interface{}{s.StatTime, s.AddressID, s.DataSize, s.StorageFee, s.Txs, s.Files}...)
	}

	sqlString := fmt.Sprintf(`
		insert into 
    		submit_topn_stats(stat_time, address_id, data_size, storage_fee, txs, files)
		values
			%s
		on duplicate key update
			stat_time = values(stat_time),
			address_id = values(address_id),                
			data_size = data_size + values(data_size),
			storage_fee = storage_fee + values(storage_fee),
			txs = txs + values(txs),
			files = files + values(files)
	`, placeholders)

	if err := db.Exec(sqlString, params...).Error; err != nil {
		return err
	}

	return nil
}

func (t *SubmitTopnStatStore) Del(dbTx *gorm.DB, minTime time.Time) error {
	db := t.DB
	if dbTx != nil {
		db = dbTx
	}
	return db.Where("stat_time < ?", minTime).Delete(&SubmitTopnStat{}).Error
}

type TopnAddress struct {
	Address    string
	DataSize   uint64
	StorageFee decimal.Decimal
	Txs        uint64
	Files      uint64
}

func (t *SubmitTopnStatStore) Topn(field string, duration time.Duration, limit int) ([]TopnAddress, error) {
	sqlStat := fmt.Sprintf(`
		SELECT
			address_id,
			IFNULL(sum(data_size), 0) data_size,
			IFNULL(sum(storage_fee), 0) storage_fee,
			IFNULL(sum(txs), 0) txs,
			IFNULL(sum(files), 0) files
		FROM submit_topn_stats
		WHERE stat_time >= ?
		GROUP BY address_id
		ORDER BY %s DESC
		LIMIT ?
	    `, field)

	stats := new([]SubmitTopnStat)
	if err := t.DB.Raw(sqlStat, time.Now().Add(-duration), limit).
		Scan(stats).Error; err != nil {
		return nil, err
	}

	addrIDs := make([]uint64, 0)
	for _, s := range *stats {
		addrIDs = append(addrIDs, s.AddressID)
	}

	addrs := new([]Address)
	err := t.DB.Raw("select * from addresses where id in ?", addrIDs).Scan(addrs).Error
	if err != nil {
		return nil, err
	}

	addrMapping := make(map[uint64]Address)
	for _, a := range *addrs {
		addrMapping[a.ID] = a
	}

	addresses := make([]TopnAddress, 0)
	for _, s := range *stats {
		ta := TopnAddress{
			Address:    addrMapping[s.AddressID].Address,
			DataSize:   s.DataSize,
			StorageFee: s.StorageFee,
			Txs:        s.Txs,
			Files:      s.Files,
		}
		addresses = append(addresses, ta)
	}

	return addresses, nil
}
