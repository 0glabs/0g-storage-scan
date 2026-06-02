package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0glabs/0g-storage-scan/metrics"
	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	set "github.com/deckarep/golang-set"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	batchSizeInsert = 100
)

var TopnSpanTypes = map[string]time.Duration{
	"24h": time.Hour * 24,
	"3d":  time.Hour * 24 * 3,
	"7d":  time.Hour * 24 * 7,
}

type MysqlStore struct {
	*mysql.Store
	*AddressStore
	*BlockStore
	*ConfigStore
	*SubmitStore
	*AddressSubmitStore
	*RewardStore
	*RewardStatStore
	*RewardTopnStatStore
	*AddressRewardStore
	*SubmitStatStore
	*SubmitTopnStatStore
	*AddressStatStore
	*MinerStore
	*MinerStatStore
	*MinerRegisterStore
	*FlowEpochStore
	*DASignerStore
	*DASignerStatStore
	*DASubmitStore
	*DARewardStore
	*DASubmitStatStore
	*DAClientStore
	*DAClientStatStore
	*RateLimitStore
	*StorageNodeTypeStore
}

func MustNewStore(db *gorm.DB, config mysql.Config) *MysqlStore {
	return &MysqlStore{
		Store:                mysql.NewStore(db),
		AddressStore:         newAddressStore(db),
		BlockStore:           newBlockStore(db),
		ConfigStore:          newConfigStore(db),
		SubmitStore:          newSubmitStore(db, config),
		AddressSubmitStore:   newAddressSubmitStore(db),
		RewardStore:          newRewardStore(db),
		RewardStatStore:      newRewardStatStore(db),
		RewardTopnStatStore:  newRewardTopnStatStore(db),
		AddressRewardStore:   newAddressRewardStore(db),
		SubmitStatStore:      newSubmitStatStore(db),
		SubmitTopnStatStore:  newSubmitTopnStatStore(db),
		AddressStatStore:     newAddressStatStore(db),
		MinerStore:           newMinerStore(db),
		MinerStatStore:       newMinerStatStore(db),
		MinerRegisterStore:   newMinerRegisterStore(db),
		FlowEpochStore:       newFlowEpochStore(db),
		DASignerStore:        newDASignerStore(db),
		DASignerStatStore:    newDASignerStatStore(db),
		DASubmitStore:        newDASubmitStore(db),
		DARewardStore:        newDARewardStore(db),
		DASubmitStatStore:    newDASubmitStatStore(db),
		DAClientStore:        newDAClientStore(db),
		DAClientStatStore:    newDAClientStatStore(db),
		RateLimitStore:       newRateLimitStore(db),
		StorageNodeTypeStore: newStorageNodeTypeStore(db),
	}
}

type DecodedLogs struct {
	Submits                    []Submit
	FlowEpochs                 []FlowEpoch
	Rewards                    []Reward
	MinerRegisters             []MinerRegister
	DASigners                  []DASigner
	DASignersWithSocketUpdated []DASigner
	DASubmits                  []DASubmit
	DASubmitsWithVerified      []DASubmit
	DARewards                  []DAReward
}

func (dl *DecodedLogs) Len() int {
	return len(dl.Submits) + +len(dl.FlowEpochs) + len(dl.Rewards) + len(dl.MinerRegisters) +
		len(dl.DASigners) + len(dl.DASignersWithSocketUpdated) + len(dl.DASubmits) +
		len(dl.DASubmitsWithVerified) + len(dl.DARewards)
}

func (ms *MysqlStore) Push(block *Block, decodedLogs *DecodedLogs) error {
	start := time.Now()
	defer metrics.Registry.Store.Push().UpdateSince(start)

	addressSubmits := make([]AddressSubmit, 0)
	if len(decodedLogs.Submits) > 0 {
		for _, submit := range decodedLogs.Submits {
			addressSubmit := AddressSubmit{
				SenderID:        submit.SenderID,
				SubmissionIndex: submit.SubmissionIndex,
				RootHash:        submit.RootHash,
				Length:          submit.Length,
				BlockNumber:     submit.BlockNumber,
				BlockTime:       submit.BlockTime,
				TxHash:          submit.TxHash,
				Fee:             submit.Fee,
				TotalSegNum:     submit.TotalSegNum,
			}
			addressSubmits = append(addressSubmits, addressSubmit)
		}
	}

	addressRewards := make([]AddressReward, 0)
	if len(decodedLogs.Rewards) > 0 {
		for _, reward := range decodedLogs.Rewards {
			addressReward := AddressReward{
				MinerID:      reward.MinerID,
				PricingIndex: reward.PricingIndex,
				Amount:       reward.Amount,
				BlockNumber:  reward.BlockNumber,
				BlockTime:    reward.BlockTime,
				TxHash:       reward.TxHash,
			}
			addressRewards = append(addressRewards, addressReward)
		}
	}

	// prepare submit table partition if necessary
	if len(decodedLogs.Submits) > 0 && ms.SubmitStore.preparePartition(decodedLogs.Submits) != nil {
		return errors.New("Failed to prepare submit partition")
	}

	return ms.Store.DB.Transaction(func(dbTx *gorm.DB) error {
		// save blocks
		if err := ms.BlockStore.Add(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to save block")
		}

		// save flow submits
		if len(decodedLogs.Submits) > 0 {
			if err := ms.SubmitStore.Add(dbTx, decodedLogs.Submits); err != nil {
				return errors.WithMessage(err, "failed to save flow submits")
			}
			if err := ms.AddressSubmitStore.Add(dbTx, addressSubmits); err != nil {
				return errors.WithMessage(err, "failed to save address flow submits")
			}
		}

		if len(decodedLogs.FlowEpochs) > 0 {
			if err := ms.FlowEpochStore.Add(dbTx, decodedLogs.FlowEpochs); err != nil {
				return errors.WithMessage(err, "failed to save flow epochs")
			}
		}

		// save distribute rewards
		if len(decodedLogs.Rewards) > 0 {
			if err := ms.RewardStore.Add(dbTx, decodedLogs.Rewards); err != nil {
				return errors.WithMessage(err, "failed to save rewards")
			}
			if err := ms.AddressRewardStore.Add(dbTx, addressRewards); err != nil {
				return errors.WithMessage(err, "failed to save address rewards")
			}
		}

		// save miner registers
		if len(decodedLogs.MinerRegisters) > 0 {
			if err := ms.MinerRegisterStore.Add(dbTx, decodedLogs.MinerRegisters); err != nil {
				return errors.WithMessage(err, "failed to save miner registers")
			}
		}

		// save DA signers
		if len(decodedLogs.DASigners) > 0 {
			if err := ms.DASignerStore.Add(dbTx, decodedLogs.DASigners); err != nil {
				return errors.WithMessage(err, "failed to save DA signers")
			}
		}

		// save DA submits
		if len(decodedLogs.DASubmits) > 0 {
			// dedup
			var daSubmits []DASubmit
			daSubmitKeySet := set.NewSet()
			for _, submit := range decodedLogs.DASubmits {
				key := fmt.Sprintf("%v_%v_%v_%v", submit.BlockNumber, submit.Epoch, submit.QuorumID, submit.RootHash)
				if !daSubmitKeySet.Contains(key) {
					daSubmits = append(daSubmits, submit)
					daSubmitKeySet.Add(key)
				}
			}
			if err := ms.DASubmitStore.Add(dbTx, daSubmits); err != nil {
				return errors.WithMessage(err, "failed to save DA submits")
			}
		}

		// update DA signers
		if len(decodedLogs.DASignersWithSocketUpdated) > 0 {
			for _, signer := range decodedLogs.DASignersWithSocketUpdated {
				if err := ms.DASignerStore.UpdateByPrimaryKey(dbTx, signer); err != nil {
					return errors.WithMessage(err, "failed to update socket for DA signer")
				}
			}
		}

		// update DA submits
		if len(decodedLogs.DASubmitsWithVerified) > 0 {
			for _, submit := range decodedLogs.DASubmitsWithVerified {
				if err := ms.DASubmitStore.UpdateByPrimaryKey(dbTx, submit); err != nil {
					return errors.WithMessage(err, "failed to update verified status for DA signer")
				}
			}
		}

		// save DA rewards
		if len(decodedLogs.DARewards) > 0 {
			if err := ms.DARewardStore.Add(dbTx, decodedLogs.DARewards); err != nil {
				return errors.WithMessage(err, "failed to save DA rewards")
			}
		}

		return nil
	})
}

func (ms *MysqlStore) Pop(block uint64) error {
	maxBlock, ok, err := ms.BlockStore.MaxBlock()
	if err != nil {
		return errors.WithMessage(err, "failed to get max block")
	}
	if !ok || block > maxBlock {
		return nil
	}
	logrus.Infof("Blockchain reorged at block %v", block)

	start := time.Now()
	defer metrics.Registry.Store.Pop().UpdateSince(start)

	return ms.Store.DB.Transaction(func(dbTx *gorm.DB) error {
		if err := ms.BlockStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove blocks")
		}
		if err := ms.SubmitStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove submits")
		}
		if err := ms.AddressSubmitStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove address submits")
		}
		if err := ms.FlowEpochStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove flow epochs")
		}
		if err := ms.RewardStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove rewards")
		}
		if err := ms.AddressRewardStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove address rewards")
		}
		if err := ms.MinerRegisterStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove miner registers")
		}
		if err := ms.DASignerStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove da signers")
		}
		if err := ms.DASubmitStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove da submits")
		}
		if err := ms.DARewardStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove da rewards")
		}
		return nil
	})
}

func (ms *MysqlStore) Close() error {
	return ms.Store.Close()
}

// GetSyncHeights returns (nodeSyncHeight, scanSyncHeight).
// It queries ConfigStore and BlockStore for the stored node sync height and max scanned block.
func (ms *MysqlStore) GetSyncHeights() (uint64, uint64, error) {
	value, ok, err := ms.ConfigStore.Get(SyncHeightNode)
	if err != nil {
		return 0, 0, errors.WithMessage(err, "Failed to get node sync height")
	}
	if !ok {
		return 0, 0, errors.New("No matching record found(node sync height)")
	}
	nodeSyncHeight, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, 0, errors.WithMessage(err, "Failed to parse node sync height")
	}

	scanSyncHeight, ok, err := ms.BlockStore.MaxBlock()
	if err != nil {
		return 0, 0, errors.WithMessage(err, "Failed to get scan sync height")
	}
	if !ok {
		return 0, 0, errors.New("No matching record found(scan sync height)")
	}

	return nodeSyncHeight, scanSyncHeight, nil
}

func (ms *MysqlStore) UpdateSubmitByPrimaryKey(s *Submit, as *AddressSubmit) error {
	return ms.Store.DB.Transaction(func(dbTx *gorm.DB) error {
		if err := ms.SubmitStore.UpdateByPrimaryKey(dbTx, s); err != nil {
			return errors.WithMessage(err, "failed to update submit")
		}
		if err := ms.AddressSubmitStore.UpdateByPrimaryKey(dbTx, as); err != nil {
			return errors.WithMessage(err, "failed to update address submit")
		}
		return nil
	})
}

func (ms *MysqlStore) UpdateFileInfos(ctx context.Context, submits []Submit, storageConfig rpc.StorageConfig) (
	map[uint64]*rpc.FileInfoResult, error) {
	params := make([]rpc.FileInfoParam, 0)
	submitMap := make(map[uint64]Submit)
	for _, submit := range submits {
		params = append(params, rpc.FileInfoParam{SubmissionIndex: submit.SubmissionIndex, Status: submit.Status})
		submitMap[submit.SubmissionIndex] = submit
	}

	result, err := rpc.BatchGetFileInfos(ctx, storageConfig, params)
	if err != nil {
		return nil, err
	}

	for _, fileInfo := range result {
		if fileInfo.Err == nil {
			d := fileInfo.Data
			s := submitMap[d.SubmissionIndex]
			submit := Submit{
				SubmissionIndex: d.SubmissionIndex,
				UploadedSegNum:  d.UploadedSegNum,
				Status:          d.Status,
			}
			addressSubmit := AddressSubmit{
				SenderID:        s.SenderID,
				SubmissionIndex: d.SubmissionIndex,
				UploadedSegNum:  d.UploadedSegNum,
				Status:          d.Status,
			}
			if err := ms.UpdateSubmitByPrimaryKey(&submit, &addressSubmit); err != nil {
				return nil, err
			}
		}
	}

	return result, nil
}

func SenderID(si uint64) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("sender_id = ?", si)
	}
}

func RootHash(rh string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("root_hash = ?", strings.ToLower(rh))
	}
}

func TxHash(rh string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("tx_hash = ?", strings.ToLower(rh))
	}
}

func StorageClass(class string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("storage_class = ?", class)
	}
}

func StatType(t string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_type = ?", t)
	}
}

func MinTimestamp(minTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_time >= ?", time.Unix(int64(minTimestamp), 0))
	}
}

func MaxTimestamp(maxTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_time <= ?", time.Unix(int64(maxTimestamp), 0))
	}
}

func MinTimestampBlockTime(minTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("block_time >= ?", time.Unix(int64(minTimestamp), 0))
	}
}

func MaxTimestampBlockTime(maxTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("block_time <= ?", time.Unix(int64(maxTimestamp), 0))
	}
}

func MinerID(mi uint64) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("miner_id = ?", mi)
	}
}
