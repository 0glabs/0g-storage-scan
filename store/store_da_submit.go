package store

import (
	"time"

	"github.com/0glabs/0g-storage-scan/contract"
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/ethereum/go-ethereum/common"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type DASubmit struct {
	BlockNumber uint64 `gorm:"primaryKey;autoIncrement:false"`
	Epoch       uint64 `gorm:"primaryKey;autoIncrement:false"`
	QuorumID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	Sender      string `gorm:"-"`
	SenderID    uint64 `gorm:"primaryKey;autoIncrement:false"`
	RootHash    string `gorm:"size:66;index:idx_root"`

	BlockTime time.Time `gorm:"not null;index:idx_bt"`
	TxHash    string    `gorm:"size:66;not null;index:idx_txHash,length:10"`

	BlobPrice           decimal.Decimal `gorm:"type:decimal(65);not null"`
	Verified            bool            `gorm:"not null;default:false"`
	BlockNumberVerified *uint64         `gorm:"index:idx_bn_verified"`
	BlockTimeVerified   *time.Time      `gorm:"index:idx_bt_verified"`
	TxHashVerified      *string         `gorm:"size:66;index:idx_txHash_verified,length:10"`
}

func NewDASubmit(blockTime time.Time, log types.Log, filter *contract.DAEntranceFilterer) (*DASubmit, error) {
	dataUpload, err := filter.ParseDataUpload(*log.ToEthLog())
	if err != nil {
		return nil, err
	}

	submit := &DASubmit{
		Epoch:     dataUpload.Epoch.Uint64(),
		QuorumID:  dataUpload.QuorumId.Uint64(),
		RootHash:  common.Hash(dataUpload.DataRoot[:]).String(),
		Sender:    dataUpload.Sender.String(),
		BlobPrice: decimal.NewFromBigInt(dataUpload.BlobPrice, 0),

		BlockNumber: log.BlockNumber,
		BlockTime:   blockTime,
		TxHash:      log.TxHash.String(),
	}

	return submit, nil
}

func NewDASubmitVerified(blockTime time.Time, log types.Log, filter *contract.DAEntranceFilterer) (*DASubmit, error) {
	commitVerified, err := filter.ParseErasureCommitmentVerified(*log.ToEthLog())
	if err != nil {
		return nil, err
	}

	txHash := log.TxHash.String()
	submit := &DASubmit{
		Epoch:    commitVerified.Epoch.Uint64(),
		QuorumID: commitVerified.QuorumId.Uint64(),
		RootHash: common.BytesToHash(commitVerified.DataRoot[:]).String(),

		Verified:            true,
		BlockNumberVerified: &log.BlockNumber,
		BlockTimeVerified:   &blockTime,
		TxHashVerified:      &txHash,
	}

	return submit, nil
}

func (DASubmit) TableName() string {
	return "da_submits"
}

type DASubmitStore struct {
	*mysql.Store
}

func newDASubmitStore(db *gorm.DB) *DASubmitStore {
	return &DASubmitStore{
		Store: mysql.NewStore(db),
	}
}

func (ss *DASubmitStore) Add(dbTx *gorm.DB, submits []DASubmit) error {
	return dbTx.CreateInBatches(submits, batchSizeInsert).Error
}

func (ss *DASubmitStore) Pop(dbTx *gorm.DB, block uint64) error {
	return dbTx.Where("block_number >= ?", block).Delete(&DASubmit{}).Error
}

func (ss *DASubmitStore) Count(startTime, endTime time.Time) (*DASubmitStatResult, error) {
	var result DASubmitStatResult
	err := ss.DB.Model(&DASubmit{}).Select(`count(*) as blobs, IFNULL(sum(blob_price), 0) as storage_fee`).
		Where("block_time >= ? and block_time < ?", startTime, endTime).Find(&result).Error
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (ss *DASubmitStore) UpdateByPrimaryKey(dbTx *gorm.DB, s DASubmit) error {
	db := ss.DB
	if dbTx != nil {
		db = dbTx
	}

	if err := db.Model(&s).Where("epoch=? and quorum_id=? and root_hash=?", s.Epoch, s.QuorumID, s.RootHash).
		Updates(s).Error; err != nil {
		return err
	}

	return nil
}

func (ss *DASubmitStore) List(rootHash *string, txHash *string, idDesc bool, skip, limit int) (int64, []DASubmit, error) {
	dbRaw := ss.DB.Model(&DASubmit{})

	var conds []func(db *gorm.DB) *gorm.DB
	if rootHash != nil {
		conds = append(conds, RootHash(*rootHash))
	}
	if txHash != nil {
		conds = append(conds, TxHash(*txHash))
	}
	dbRaw.Scopes(conds...)

	var orderBy string
	if idDesc {
		orderBy = "block_number DESC, epoch DESC, quorum_id DESC"
	} else {
		orderBy = "block_number ASC, epoch ASC, quorum_id ASC"
	}

	list := new([]DASubmit)
	total, err := ss.Store.ListByOrder(dbRaw, orderBy, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}

func (ss *DASubmitStore) CountActive(startTime, endTime time.Time) (uint64, error) {
	db := ss.DB.Model(&DASubmit{})

	nilTime := time.Time{}
	if startTime != nilTime && endTime != nilTime {
		db = db.Where("block_time >= ? and block_time < ?", startTime, endTime)
	}
	if startTime == nilTime && endTime != nilTime {
		db = db.Where("block_time < ?", endTime)
	}

	var clientActive int64
	err := db.Select(`count(distinct sender_id) as client_active`).
		Find(&clientActive).Error
	if err != nil {
		return 0, err
	}

	return uint64(clientActive), nil
}

type DASubmitStat struct {
	ID       uint64    `json:"-"`
	StatType string    `gorm:"size:4;not null;uniqueIndex:idx_statType_statTime,priority:1" json:"-"`
	StatTime time.Time `gorm:"not null;uniqueIndex:idx_statType_statTime,priority:2" json:"statTime"`

	BlobNew         uint64          `gorm:"not null;default:0" json:"blobNew"`                          // Number of blobs in a specific time interval
	BlobTotal       uint64          `gorm:"not null;default:0" json:"blobTotal"`                        // Total number of blobs by a certain time
	DataSizeNew     uint64          `gorm:"type:decimal(65);not null;default:0" json:"dataSizeNew"`     // The data size of storage
	DataSizeTotal   uint64          `gorm:"type:decimal(65);not null;default:0" json:"dataSizeTotal"`   // The total data size of storage
	StorageFeeNew   decimal.Decimal `gorm:"type:decimal(65);not null;default:0" json:"storageFeeNew"`   // The fee for storage
	StorageFeeTotal decimal.Decimal `gorm:"type:decimal(65);not null;default:0" json:"storageFeeTotal"` // The total fee for storage
}

func (DASubmitStat) TableName() string {
	return "da_submit_stats"
}

type DASubmitStatStore struct {
	*mysql.Store
}

func newDASubmitStatStore(db *gorm.DB) *DASubmitStatStore {
	return &DASubmitStatStore{
		Store: mysql.NewStore(db),
	}
}

func (t *DASubmitStatStore) LastByType(statType string) (*DASubmitStat, error) {
	var submitStat DASubmitStat
	err := t.Store.DB.Where("stat_type = ?", statType).Order("stat_time desc").Last(&submitStat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &submitStat, nil
}

type DASubmitStatResult struct {
	Blobs      uint64
	StorageFee decimal.Decimal
}

func (t *DASubmitStatStore) Sum(startTime, endTime time.Time, statType string) (*DASubmitStatResult, error) {
	nilTime := time.Time{}
	if startTime == nilTime && endTime == nilTime {
		return nil, errors.New("At least provide one parameter for startTime and endTime")
	}

	db := t.DB.Model(&DASubmitStat{}).Select(`IFNULL(sum(blob_new), 0) as blobs, 
		IFNULL(sum(storage_fee_new), 0) as storage_fee`)
	if startTime != nilTime && endTime != nilTime {
		db = db.Where("stat_type = ? and stat_time >= ? and stat_time < ?", statType, startTime, endTime)
	}
	if startTime != nilTime && endTime == nilTime {
		db = db.Where("stat_type = ? and stat_time >= ?", statType, startTime)
	}
	if startTime == nilTime && endTime != nilTime {
		db = db.Where("stat_type = ? and stat_time < ?", statType, endTime)
	}

	var sum DASubmitStatResult
	err := db.Find(&sum).Error
	if err != nil {
		return nil, err
	}

	return &sum, nil
}

func (t *DASubmitStatStore) Add(dbTx *gorm.DB, submitStat []*DASubmitStat) error {
	return dbTx.CreateInBatches(submitStat, batchSizeInsert).Error
}

func (t *DASubmitStatStore) Del(dbTx *gorm.DB, submitStat *DASubmitStat) error {
	return dbTx.Where("stat_type = ? and stat_time = ?", submitStat.StatType, submitStat.StatTime).Delete(&DASubmitStat{}).Error
}

func (t *DASubmitStatStore) List(intervalType *string, minTimestamp, maxTimestamp *int, desc bool, skip, limit int) (int64,
	[]DASubmitStat, error) {
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

	dbRaw := t.DB.Model(&DASubmitStat{})
	dbRaw.Scopes(conds...)

	list := new([]DASubmitStat)
	total, err := t.Store.List(dbRaw, desc, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}
