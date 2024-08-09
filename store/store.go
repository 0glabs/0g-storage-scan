package store

import (
	"fmt"
	"strings"

	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	set "github.com/deckarep/golang-set"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

const (
	batchSizeInsert = 100
)

type MysqlStore struct {
	*mysql.Store
	*AddressStore
	*BlockStore
	*ConfigStore
	*SubmitStore
	*AddressSubmitStore
	*RewardStore
	*AddressRewardStore
	*SubmitStatStore
	*AddressStatStore
	*MinerStore
	*MinerStatStore
	*DASignerStore
	*DASignerStatStore
	*DASubmitStore
	*DARewardStore
	*DASubmitStatStore
	*DAClientStore
	*DAClientStatStore
}

func MustNewStore(db *gorm.DB) *MysqlStore {
	return &MysqlStore{
		Store:              mysql.NewStore(db),
		AddressStore:       newAddressStore(db),
		BlockStore:         newBlockStore(db),
		ConfigStore:        newConfigStore(db),
		SubmitStore:        newSubmitStore(db),
		AddressSubmitStore: newAddressSubmitStore(db),
		RewardStore:        newRewardStore(db),
		AddressRewardStore: newAddressRewardStore(db),
		SubmitStatStore:    newSubmitStatStore(db),
		AddressStatStore:   newAddressStatStore(db),
		MinerStore:         newMinerStore(db),
		MinerStatStore:     newMinerStatStore(db),
		DASignerStore:      newDASignerStore(db),
		DASignerStatStore:  newDASignerStatStore(db),
		DASubmitStore:      newDASubmitStore(db),
		DARewardStore:      newDARewardStore(db),
		DASubmitStatStore:  newDASubmitStatStore(db),
		DAClientStore:      newDAClientStore(db),
		DAClientStatStore:  newDAClientStatStore(db),
	}
}

type DecodedLogs struct {
	Submits                    []Submit
	Rewards                    []Reward
	DASigners                  []DASigner
	DASignersWithSocketUpdated []DASigner
	DASubmits                  []DASubmit
	DASubmitsWithVerified      []DASubmit
	DARewards                  []DAReward
}

func (ms *MysqlStore) Push(block *Block, decodedLogs *DecodedLogs) error {
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

		// save distribute rewards
		if len(decodedLogs.Rewards) > 0 {
			if err := ms.RewardStore.Add(dbTx, decodedLogs.Rewards); err != nil {
				return errors.WithMessage(err, "failed to save rewards")
			}
			if err := ms.AddressRewardStore.Add(dbTx, addressRewards); err != nil {
				return errors.WithMessage(err, "failed to save address rewards")
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

		// save DA submits
		if len(decodedLogs.DARewards) > 0 {
			if err := ms.DARewardStore.Add(dbTx, decodedLogs.DARewards); err != nil {
				return errors.WithMessage(err, "failed to save DA rewards")
			}
		}

		return nil
	})
}

func (ms *MysqlStore) Pop(block uint64) error {
	maxBlock, ok, err := ms.MaxBlock()
	if err != nil {
		return errors.WithMessage(err, "failed to get max block")
	}
	if !ok || block > maxBlock {
		return nil
	}

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
		if err := ms.RewardStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove rewards")
		}
		if err := ms.AddressRewardStore.Pop(dbTx, block); err != nil {
			return errors.WithMessage(err, "failed to remove address rewards")
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

func StatType(t string) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_type = ?", t)
	}
}

func MinTimestamp(minTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_time >= ?", minTimestamp)
	}
}

func MaxTimestamp(maxTimestamp int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("stat_time <= ?", maxTimestamp)
	}
}

func MinerID(mi uint64) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		return db.Where("miner_id = ?", mi)
	}
}
