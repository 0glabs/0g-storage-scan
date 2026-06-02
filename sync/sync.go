package sync

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/0glabs/0g-storage-scan/metrics"
	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/openweb3/web3go"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type SyncConfig struct {
	EthURL                   string
	BlockWhenFlowCreated     uint64 `default:"0"`
	DelayBlocksAgainstLatest uint64 `default:"3"`
	BatchBlocksOnCatchup     uint64 `default:"0"`
	BatchBlocksOnBatchCall   uint64 `default:"16"`
}

type Syncer struct {
	baseSyncer
	latestBlock uint64

	syncIntervalNormal  time.Duration
	syncIntervalCatchUp time.Duration
	catchupSyncer       *CatchupSyncer
	storageSyncer       *StorageSyncer
	patchSyncer         *PatchSyncer
}

// MustNewSyncer creates an instance of Syncer to sync blockchain data.
func MustNewSyncer(sdk *web3go.Client, db *store.MysqlStore, cf SyncConfig, cs *CatchupSyncer, ss *StorageSyncer,
	ps *PatchSyncer) *Syncer {
	base := baseSyncer{
		conf: &cf,
		sdk:  sdk,
		db:   db,
	}

	base.mustInit()

	syncer := &Syncer{
		baseSyncer:          base,
		syncIntervalNormal:  5 * time.Second,
		syncIntervalCatchUp: 5 * time.Second,
		catchupSyncer:       cs,
		storageSyncer:       ss,
		patchSyncer:         ps,
	}

	// Load last sync block information
	syncer.mustLoadLastSyncBlock()

	return syncer
}

// Load last sync block from database to continue synchronization.
func (s *Syncer) mustLoadLastSyncBlock() {
	loaded, err := s.loadLastSyncBlock()
	if err != nil {
		logrus.WithError(err).Fatal("Failed to load last sync block from db")
	}

	// Load db sync start block config on initial loading if necessary.
	if !loaded && s.conf != nil {
		s.currentBlock = s.conf.BlockWhenFlowCreated
	}
}

func (s *Syncer) loadLastSyncBlock() (loaded bool, err error) {
	maxBlock, ok, err := s.db.BlockStore.MaxBlock()
	if err != nil {
		return false, errors.WithMessage(err, "Failed to get max block from block table")
	}

	if ok {
		s.currentBlock = maxBlock + 1
	}

	return ok, nil
}

func (s *Syncer) Sync(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()

	err := s.checkReorgData()
	if err != nil {
		logrus.WithError(err).Panic("Failed to check reorg data")
	}

	go s.storageSyncer.Sync(ctx, s.storageSyncer.LatestFiles)
	go s.storageSyncer.Sync(ctx, s.storageSyncer.NodeSyncHeight)
	go s.storageSyncer.Sync(ctx, s.storageSyncer.ReconcileStorageClass)
	go s.storageSyncer.Sync(ctx, s.storageSyncer.RefreshNodeTypes)
	go s.patchSyncer.Sync(ctx, s.patchSyncer.L1Txs)
	go s.patchSyncer.Sync(ctx, s.patchSyncer.MinerAttempts)

	s.catchupSyncer.Sync(ctx)
	s.currentBlock = s.catchupSyncer.finalizedBlock + 1
	logrus.WithField("block", s.catchupSyncer.finalizedBlock).Info("Catchup syncer done")

	ticker := time.NewTicker(s.syncIntervalCatchUp)
	defer ticker.Stop()

	logrus.Info("Syncer starting to sync data")
	for {
		select {
		case <-ctx.Done():
			logrus.Info("Syncer shutdown ok")
			return
		case <-ticker.C:
			if err := s.doTicker(ctx, ticker); err != nil {
				logrus.WithError(err).
					WithField("currentBlock", s.currentBlock).
					Warn("Failed to sync data")
			}
		}
	}
}

func (s *Syncer) doTicker(ctx context.Context, ticker *time.Ticker) error {
	logrus.Debug("Syncer ticking")

	start := time.Now()
	complete, err := s.syncOnce(ctx)
	metrics.Registry.Sync.SyncOnceQps(err).UpdateSince(start)

	switch {
	case err != nil:
		ticker.Reset(s.syncIntervalNormal)
		return err
	case complete:
		ticker.Reset(s.syncIntervalNormal)
	default:
		ticker.Reset(s.syncIntervalCatchUp)
	}

	return nil
}

var (
	checkParityAPIAlready bool
	syncDataByLogs        bool
)

func (s *Syncer) syncOnce(ctx context.Context) (bool, error) {
	// get the latest block
	latestBlock, err := s.sdk.Eth.BlockNumber()
	if s.catchupSyncer.alertChannel != "" {
		e := err
		if e == nil && latestBlock.Uint64() <= s.latestBlock {
			e = errors.Errorf("Blockchain height stops growing at %v", latestBlock)
		}
		if e = rpc.AlertErr(ctx, "BlockchainRPCError", s.catchupSyncer.alertChannel, e,
			s.catchupSyncer.healthReport, &s.catchupSyncer.nodeRpcHealth, s.conf.EthURL); e != nil {
			return false, e
		}
	}
	if err != nil {
		return false, errors.WithMessagef(err, "Failed to get the latest block number")
	}
	s.latestBlock = latestBlock.Uint64()

	// check latest block
	curBlock := s.currentBlock
	maxSyncBlock := latestBlock.Uint64() - s.conf.DelayBlocksAgainstLatest
	if curBlock > maxSyncBlock {
		return true, nil
	}

	// calculate batch size based on block gap
	blockGap := maxSyncBlock - curBlock + 1 // +1 to include the target block
	batchSize := s.calculateBatchSize(blockGap)

	// calculate the actual range to sync
	endBlock := min(curBlock+batchSize-1, maxSyncBlock)

	// check parity api available
	if err := s.tryParityAPI(ctx, curBlock); err != nil {
		return false, err
	}

	// get batch eth data
	var dataArray []*rpc.EthData
	if syncDataByLogs {
		dataArray, err = rpc.GetEthDataBatchByLogs(s.sdk, curBlock, endBlock, s.addresses, s.topics)
	} else {
		dataArray, err = rpc.GetEthDataBatchByReceipts(s.sdk, curBlock, endBlock)
	}
	if err != nil {
		return false, err
	}

	if len(dataArray) == 0 {
		return true, nil
	}

	// Check for reorg before processing the batch
	if len(dataArray) > 0 {
		firstData := dataArray[0]
		latestBlockHash, err := s.getStoreLatestBlockHash()
		if err != nil {
			return false, err
		}
		if len(latestBlockHash) > 0 && firstData.Block.ParentHash.Hex() != latestBlockHash {
			return false, s.revertReorgData(s.latestStoreBlock())
		}
	}

	// process each block in the batch
	syncedBlocks := 0
	for _, data := range dataArray {
		// persist db
		block := store.NewBlock(data.Block)
		decodedLogs, err := s.parseEthData(data)
		if err != nil {
			return false, err
		}
		metrics.Registry.Sync.SyncOnceSize().Update(int64(decodedLogs.Len()))
		if err = s.db.Push(block, decodedLogs); err != nil {
			return false, err
		}

		syncedBlocks++
		s.currentBlock++
	}

	// log progress
	if syncedBlocks > 1 {
		logrus.WithFields(logrus.Fields{
			"blocks_synced": syncedBlocks,
			"current_block": s.currentBlock - 1,
			"latest_block":  latestBlock.Uint64(),
			"block_gap":     blockGap,
			"batch_size":    batchSize,
		}).Info("Batch sync completed")
	} else if s.currentBlock%100 == 0 {
		logrus.WithField("block", s.currentBlock-1).Info("Sync data")
	}

	// Check if we're now caught up after syncing
	if s.currentBlock >= latestBlock.Uint64()-s.conf.DelayBlocksAgainstLatest {
		return true, nil
	}

	return false, nil
}

// calculateBatchSize determines the number of blocks to sync based on the gap
func (s *Syncer) calculateBatchSize(blockGap uint64) uint64 {
	if blockGap == 0 {
		return 1 // Always sync at least 1 block
	}
	if blockGap > 2000 {
		return 1000
	}
	return blockGap
}

func (s *Syncer) getStoreLatestBlockHash() (string, error) {
	blockFlowContractCreated := s.conf.BlockWhenFlowCreated
	if s.currentBlock <= blockFlowContractCreated {
		return "", nil
	}

	latestBlockNo := s.latestStoreBlock()
	hash, _, err := s.db.BlockHash(latestBlockNo)
	return hash, err
}

func (s *Syncer) latestStoreBlock() uint64 {
	if s.currentBlock > 0 {
		return s.currentBlock - 1
	}

	return 0
}

// Ideally, one by one block to check and prune reorg data is fine though, this could be improved with binary search probing.
func (s *Syncer) checkReorgData() error {
	for {
		blockNum, ok, err := s.db.BlockStore.MaxBlock()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}

		exist, err := s.existReorgData(blockNum)
		if err != nil {
			return err
		}

		if !exist {
			break
		}

		err = s.revertReorgData(blockNum)
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Syncer) existReorgData(blockNum uint64) (bool, error) {
	hash, ok, err := s.db.BlockHash(blockNum)
	if err != nil {
		return false, errors.WithMessagef(err, "Failed to get block at %v from db", blockNum)
	}
	if !ok {
		return false, nil
	}

	block, err := rpc.GetBlockByNumber(s.sdk, types.BlockNumber(blockNum), false)
	if err != nil {
		return false, err
	}

	return hash != block.Hash.String(), nil
}

func (s *Syncer) revertReorgData(revertBlock uint64) error {
	// pop from db
	logrus.WithField("block", revertBlock).Info("Revert eth data")
	if err := s.db.Pop(revertBlock); err != nil {
		return errors.WithMessage(err, "Failed to pop eth data from db")
	}

	// update currentBlock
	s.currentBlock = revertBlock

	return nil
}

func (s *Syncer) parseEthData(data *rpc.EthData) (*store.DecodedLogs, error) {
	var logs []types.Log
	if syncDataByLogs {
		logs = data.Logs
	} else {
		for _, t := range data.Block.Transactions.Transactions() {
			rcpt := data.Receipts[t.Hash]
			if rcpt == nil || !rpc.IsTxExecutedInBlock(t, *rcpt) {
				continue
			}
			for _, log := range rcpt.Logs {
				logs = append(logs, *log)
			}
		}
	}

	bn2TimeMap := map[uint64]uint64{data.Block.Number.Uint64(): data.Block.Timestamp}

	decodedLogs, err := s.convertLogs(logs, bn2TimeMap)
	if err != nil {
		return nil, err
	}

	return decodedLogs, nil
}

// check parity api available
func (s *Syncer) tryParityAPI(ctx context.Context, curBlock uint64) error {
	if !checkParityAPIAlready {
		_, err := rpc.GetEthDataByReceipts(s.sdk, curBlock)

		if s.catchupSyncer.alertChannel != "" {
			if e := rpc.AlertErr(ctx, "BlockchainRPCError", s.catchupSyncer.alertChannel, err,
				s.catchupSyncer.healthReport, &s.catchupSyncer.nodeRpcHealth, s.conf.EthURL); e != nil {
				return e
			}
		}

		if err != nil {
			if strings.Contains(err.Error(), "parity_getBlockReceipts") {
				syncDataByLogs = true
			} else {
				return err
			}
		}

		checkParityAPIAlready = true
	}

	return nil
}
