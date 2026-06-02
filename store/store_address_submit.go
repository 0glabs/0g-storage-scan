package store

import (
	"time"

	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/pkg/errors"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type AddressSubmit struct {
	SenderID        uint64 `gorm:"primaryKey;autoIncrement:false"`
	SubmissionIndex uint64 `gorm:"primaryKey;autoIncrement:false"`
	RootHash        string `gorm:"size:66;index:idx_root"`
	Length          uint64 `gorm:"not null"`

	BlockNumber uint64    `gorm:"not null;index:idx_bn"`
	BlockTime   time.Time `gorm:"not null;index:idx_bt"`
	TxHash      string    `gorm:"size:66;not null;index:idx_tx_hash"`

	TotalSegNum    uint64          `gorm:"not null;default:0"`
	UploadedSegNum uint64          `gorm:"not null;default:0"`
	Status         uint8           `gorm:"not null;default:0"`
	Fee            decimal.Decimal `gorm:"type:decimal(65);not null"`

	// StorageClass mirrors Submit.StorageClass for per-address (My Files) queries.
	StorageClass string `gorm:"size:20;not null;default:standard;index"`
}

func (AddressSubmit) TableName() string {
	return "address_submits"
}

type AddressSubmitStore struct {
	*mysql.Store
}

func newAddressSubmitStore(db *gorm.DB) *AddressSubmitStore {
	return &AddressSubmitStore{
		Store: mysql.NewStore(db),
	}
}

func (ass *AddressSubmitStore) Add(dbTx *gorm.DB, addressSubmits []AddressSubmit) error {
	return dbTx.CreateInBatches(addressSubmits, batchSizeInsert).Error
}

func (ass *AddressSubmitStore) Pop(dbTx *gorm.DB, block uint64) error {
	return dbTx.Where("block_number >= ?", block).Delete(&AddressSubmit{}).Error
}

func (ass *AddressSubmitStore) UpdateByPrimaryKey(dbTx *gorm.DB, s *AddressSubmit) error {
	db := ass.DB
	if dbTx != nil {
		db = dbTx
	}

	if err := db.Model(&s).Where("sender_id=? and submission_index=?", s.SenderID, s.SubmissionIndex).
		Updates(s).Error; err != nil {
		return err
	}

	return nil
}

// SetStorageClassByRootHashes mirrors SubmitStore.SetStorageClassByRootHashes
// for the per-address table so My Files stays consistent with the global view.
func (ass *AddressSubmitStore) SetStorageClassByRootHashes(class string, rootHashes []string) (int64, error) {
	if len(rootHashes) == 0 {
		return 0, nil
	}
	res := ass.DB.Model(&AddressSubmit{}).
		Where("root_hash IN ?", rootHashes).
		Where("storage_class <> ?", class).
		Update("storage_class", class)
	return res.RowsAffected, res.Error
}

func (ass *AddressSubmitStore) List(addressID *uint64, rootHash *string, txHash *string, storageClass *string,
	minTimestamp, maxTimestamp *int, idDesc bool, skip, limit int) (
	int64, []AddressSubmit, error) {
	if addressID == nil {
		return 0, nil, errors.New("nil addressID")
	}

	dbRaw := ass.DB.Model(&AddressSubmit{})
	var conds []func(db *gorm.DB) *gorm.DB
	conds = append(conds, SenderID(*addressID))
	if rootHash != nil {
		conds = append(conds, RootHash(*rootHash))
	}
	if txHash != nil {
		conds = append(conds, TxHash(*txHash))
	}
	if storageClass != nil {
		conds = append(conds, StorageClass(*storageClass))
	}
	if minTimestamp != nil {
		conds = append(conds, MinTimestampBlockTime(*minTimestamp))
	}
	if maxTimestamp != nil {
		conds = append(conds, MaxTimestampBlockTime(*maxTimestamp))
	}
	dbRaw.Scopes(conds...)

	var orderBy string
	if idDesc {
		orderBy = "submission_index DESC"
	} else {
		orderBy = "submission_index ASC"
	}

	list := new([]AddressSubmit)
	if len(conds) == 1 {
		var address Address
		exist, err := ass.Store.GetById(&address, *addressID)
		if err != nil {
			return 0, nil, err
		}
		if !exist {
			return 0, nil, errors.New("address info not exists")
		}

		total := int64(address.Files)
		if total <= int64(skip) {
			return total, *list, nil
		}

		if err := ass.DB.Raw(`
			SELECT a.* FROM address_submits a
			JOIN (
				SELECT sender_id, submission_index
				FROM address_submits
				WHERE sender_id = ?
				ORDER BY submission_index DESC
				LIMIT ? OFFSET ? 
			) AS b ON a.sender_id = b.sender_id AND a.submission_index = b.submission_index
			ORDER BY a.submission_index DESC
		`, *addressID, limit, skip).Scan(list).Error; err != nil {
			return 0, nil, err
		}
		return total, *list, nil
	}

	total, err := ass.Store.ListByOrder(dbRaw, orderBy, skip, limit, list)
	if err != nil {
		return 0, nil, err
	}

	return total, *list, nil
}

func (ass *AddressSubmitStore) Count(addressID *uint64) (*SubmitStatResult, error) {
	if addressID == nil {
		return nil, errors.New("nil addressID")
	}

	var result SubmitStatResult
	err := ass.DB.Model(&AddressSubmit{}).Select(`count(submission_index) as file_count,
		IFNULL(sum(length), 0) as data_size, IFNULL(sum(fee), 0) as base_fee, count(distinct tx_hash) as tx_count`).
		Where("sender_id = ?", addressID).Find(&result).Error
	if err != nil {
		return nil, err
	}

	return &result, nil
}

type AddressSubmitStatsResult struct {
	TotalFiles        int64
	TotalBytes        uint64
	TotalStorageFee   decimal.Decimal
	ExpiredFiles      int64
	ExpiringSoonFiles int64
	HealthyFiles      int64
}

// expiringSoonSeconds is the lookahead window (in seconds) used to classify a file as "expiring soon".
const expiringSoonSeconds = uint64(7 * 24 * 3600) // 7 days

func (ass *AddressSubmitStore) Stats(addressID uint64, expireSeconds uint64) (*AddressSubmitStatsResult, error) {
	var result AddressSubmitStatsResult
	err := ass.DB.Model(&AddressSubmit{}).
		Select(`COUNT(*) as total_files,
			IFNULL(SUM(length), 0) as total_bytes,
			IFNULL(SUM(fee), 0) as total_storage_fee,
			SUM(CASE WHEN UNIX_TIMESTAMP(block_time) + ? < UNIX_TIMESTAMP(NOW()) THEN 1 ELSE 0 END) as expired_files,
			SUM(CASE WHEN UNIX_TIMESTAMP(block_time) + ? >= UNIX_TIMESTAMP(NOW())
				AND UNIX_TIMESTAMP(block_time) + ? < UNIX_TIMESTAMP(NOW()) + ?
				THEN 1 ELSE 0 END) as expiring_soon_files,
			SUM(CASE WHEN UNIX_TIMESTAMP(block_time) + ? >= UNIX_TIMESTAMP(NOW()) + ?
				THEN 1 ELSE 0 END) as healthy_files`,
			expireSeconds,
			expireSeconds, expireSeconds, expiringSoonSeconds,
			expireSeconds, expiringSoonSeconds).
		Where("sender_id = ?", addressID).
		Find(&result).Error
	if err != nil {
		return nil, err
	}
	return &result, nil
}
