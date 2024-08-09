package store

import (
	"time"

	"github.com/0glabs/0g-storage-scan/contract"
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

type DASigner struct {
	SignerID uint64 `gorm:"primaryKey;autoIncrement:false"`
	Address  string `gorm:"_"`
	Socket   string `gorm:"size:128"`

	BlockNumber uint64    `gorm:"not null;index:idx_bn"`
	BlockTime   time.Time `gorm:"not null;index:idx_bt"`
	TxHash      string    `gorm:"size:66;not null;index:idx_txHash,length:10"`
}

func NewDASigner(blockTime time.Time, signerLog types.Log, filter *contract.DASignersFilterer) (*DASigner, error) {
	newSigner, err := filter.ParseNewSigner(*signerLog.ToEthLog())
	if err != nil {
		return nil, err
	}

	signer := &DASigner{
		Address:     newSigner.Signer.String(),
		BlockNumber: signerLog.BlockNumber,
		BlockTime:   blockTime,
		TxHash:      signerLog.TxHash.String(),
	}

	return signer, nil
}

func NewDASignerSocket(signerLog types.Log, filter *contract.DASignersFilterer) (*DASigner, error) {
	socketUpdated, err := filter.ParseSocketUpdated(*signerLog.ToEthLog())
	if err != nil {
		return nil, err
	}

	signer := &DASigner{
		Address: socketUpdated.Signer.String(),
		Socket:  socketUpdated.Socket,
	}

	return signer, nil
}

func (DASigner) TableName() string {
	return "da_signers"
}

type DASignerStore struct {
	*mysql.Store
}

func newDASignerStore(db *gorm.DB) *DASignerStore {
	return &DASignerStore{
		Store: mysql.NewStore(db),
	}
}

func (ss *DASignerStore) Add(dbTx *gorm.DB, signers []DASigner) error {
	return dbTx.CreateInBatches(signers, batchSizeInsert).Error
}

func (ss *DASignerStore) Pop(dbTx *gorm.DB, block uint64) error {
	return dbTx.Where("block_number >= ?", block).Delete(&DASigner{}).Error
}

func (ss *DASignerStore) UpdateByPrimaryKey(dbTx *gorm.DB, s DASigner) error {
	db := ss.DB
	if dbTx != nil {
		db = dbTx
	}

	if err := db.Model(&s).Where("signer_id=?", s.SignerID).Updates(s).Error; err != nil {
		return err
	}

	return nil
}

func (ss *DASignerStore) Count(startTime, endTime time.Time) (uint64, error) {
	db := ss.DB.Model(&DASigner{})
	nilTime := time.Time{}
	if startTime != nilTime && endTime != nilTime {
		db = db.Where("block_time >= ? and block_time < ?", startTime, endTime)
	}
	if startTime == nilTime && endTime != nilTime {
		db = db.Where("block_time < ?", endTime)
	}

	var count int64
	err := db.Count(&count).Error
	if err != nil {
		return 0, err
	}

	return uint64(count), nil
}

type DASignerStat struct {
	ID       uint64    `json:"-"`
	StatType string    `gorm:"size:4;not null;uniqueIndex:idx_statType_statTime,priority:1" json:"-"`
	StatTime time.Time `gorm:"not null;uniqueIndex:idx_statType_statTime,priority:2" json:"statTime"`

	SignerNew    uint64 `gorm:"not null;default:0" json:"signerNew"`    // Number of da signer in a specific time interval
	SignerActive uint64 `gorm:"not null;default:0" json:"signerActive"` // Number of active da signer in a specific time interval
	SignerTotal  uint64 `gorm:"not null;default:0" json:"signerTotal"`  // Total number of da signer by a certain time
}

func (DASignerStat) TableName() string {
	return "da_signer_stats"
}

type DASignerStatStore struct {
	*mysql.Store
}

func newDASignerStatStore(db *gorm.DB) *DASignerStatStore {
	return &DASignerStatStore{
		Store: mysql.NewStore(db),
	}
}

func (t *DASignerStatStore) LastByType(statType string) (*DASignerStat, error) {
	var daSignerStat DASignerStat
	err := t.Store.DB.Where("stat_type = ?", statType).Order("stat_time desc").Last(&daSignerStat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &daSignerStat, nil
}

func (t *DASignerStatStore) Add(dbTx *gorm.DB, daSignerStats []*DASignerStat) error {
	return dbTx.CreateInBatches(daSignerStats, batchSizeInsert).Error
}

func (t *DASignerStatStore) Del(dbTx *gorm.DB, daSignerStat *DASignerStat) error {
	return dbTx.Where("stat_type = ? and stat_time = ?", daSignerStat.StatType, daSignerStat.StatTime).Delete(&DASignerStat{}).Error
}

func (t *DASignerStatStore) List(intervalType *string, minTimestamp, maxTimestamp *int, desc bool, skip, limit int) (int64,
	[]DASignerStat, error) {
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

	dbRaw := t.DB.Model(&DASignerStat{})
	dbRaw.Scopes(conds...)

	list := new([]DASignerStat)
	total, err := t.Store.List(dbRaw, desc, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}
