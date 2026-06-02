package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/health"
	"github.com/openweb3/web3go"
	"github.com/sirupsen/logrus"
)

var (
	BatchGetSubmitsLatest = 100
	intervalNormal        = time.Second
	intervalException     = time.Second * 10
)

type StorageSyncer struct {
	db               *store.MysqlStore
	storageConfig    rpc.StorageConfig
	alertChannel     string
	healthReport     health.TimedCounterConfig
	storageRpcHealth health.TimedCounter
	blockchainClient *web3go.Client
}

func MustNewStorageSyncer(db *store.MysqlStore, storageConfig rpc.StorageConfig, alertChannel string,
	healthReport health.TimedCounterConfig, blockchainClient *web3go.Client) *StorageSyncer {
	return &StorageSyncer{
		db:               db,
		storageConfig:    storageConfig,
		alertChannel:     alertChannel,
		healthReport:     healthReport,
		storageRpcHealth: health.TimedCounter{},
		blockchainClient: blockchainClient,
	}
}

func (ss *StorageSyncer) Sync(ctx context.Context, f func(ctx context.Context, ticker *time.Ticker)) {
	ticker := time.NewTicker(intervalNormal)
	defer ticker.Stop()

	logrus.Info("Storage syncer starting to sync data.")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f(ctx, ticker)
		}
	}
}

func (ss *StorageSyncer) LatestFiles(ctx context.Context, ticker *time.Ticker) {
	if interrupted(ctx) {
		return
	}

	submits, err := ss.db.SubmitStore.QueryDesc(BatchGetSubmitsLatest)
	if err != nil {
		ticker.Reset(intervalException)
		return
	}

	if len(submits) == 0 {
		return
	}

	unfinalized := make([]store.Submit, 0)
	for _, submit := range submits {
		if submit.Status < uint8(rpc.Pruned) {
			unfinalized = append(unfinalized, submit)
		}
	}

	if len(unfinalized) == 0 {
		return
	}

	if _, err := ss.db.UpdateFileInfos(ctx, unfinalized, ss.storageConfig); err != nil {
		ticker.Reset(intervalException)
	}
}

func (ss *StorageSyncer) NodeSyncHeight(ctx context.Context, ticker *time.Ticker) {
	var err error
	nodeStatus, err := rpc.GetNodeStatus(ss.storageConfig)

	if err == nil {
		height := nodeStatus.LogSyncHeight
		err = ss.db.ConfigStore.Upsert(nil, store.SyncHeightNode, strconv.FormatUint(height, 10))
		if err != nil {
			logrus.WithError(err).Error("Failed to upsert storage node sync height")
		} else {
			// Check sync height gaps and use gap error for alerting if present
			err = ss.checkSyncHeightGaps(height)
		}
	}

	if ss.alertChannel != "" {
		e := rpc.AlertErr(ctx, "StorageIndexerRPCError", ss.alertChannel, err, ss.healthReport,
			&ss.storageRpcHealth, ss.storageConfig.Indexer)

		if e != nil {
			ticker.Reset(intervalException)
			logrus.WithError(err).Error("Failed to alert storage node status")
		} else {
			ticker.Reset(intervalNormal)
		}
	}
}

// ReconcileStorageClass refreshes each file's hot/standard storage class by
// reconciling against the hot router's current cache set (the "hot set"). Every
// file defaults to "standard"; this worker promotes files present in the hot set
// to "hot" and demotes files that have been evicted (currently "hot" in the DB
// but no longer in the hot set) back to "standard". It is the source of truth for
// the label and self-heals after chain reorgs, missed events, and the
// cache-before-index race. When no hot router is configured it is a no-op.
func (ss *StorageSyncer) ReconcileStorageClass(ctx context.Context, ticker *time.Ticker) {
	if interrupted(ctx) {
		return
	}

	cfg := ss.storageConfig
	if cfg.HotRouter == "" {
		// Feature disabled; idle on a slow tick so we don't spin at intervalNormal.
		ticker.Reset(time.Minute)
		return
	}

	interval := cfg.ClassReconcileInterval
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	limit := cfg.ClassPageLimit
	if limit <= 0 {
		limit = 2000
	}

	// 1. Page the router's full hot set. On any error, skip this cycle WITHOUT
	//    demoting — a transient router outage must not mass-flip files to standard.
	hotSet := make(map[string]struct{})
	cursor := ""
	for {
		hashes, next, err := rpc.ListCachedFiles(cfg, cursor, limit)
		if err != nil {
			logrus.WithError(err).Warn("Failed to fetch hot set from router; skipping storage-class reconcile")
			ticker.Reset(intervalException)
			return
		}
		for _, h := range hashes {
			hotSet[strings.ToLower(h)] = struct{}{}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	// 2. Promote files in the hot set to "hot" (the <> guard makes this near-zero-write
	//    in steady state).
	hotList := make([]string, 0, len(hotSet))
	for h := range hotSet {
		hotList = append(hotList, h)
	}
	if err := ss.setStorageClassChunked(store.StorageClassHot, hotList); err != nil {
		logrus.WithError(err).Error("Failed to promote files to storage_class=hot")
		ticker.Reset(intervalException)
		return
	}

	// 3. Demote evicted files: currently "hot" in the DB but absent from the hot set.
	currentHot, err := ss.db.SubmitStore.ListHotRootHashes()
	if err != nil {
		logrus.WithError(err).Error("Failed to list currently-hot files for demotion")
		ticker.Reset(intervalException)
		return
	}
	toDemote := make([]string, 0)
	for _, h := range currentHot {
		if _, ok := hotSet[strings.ToLower(h)]; !ok {
			toDemote = append(toDemote, h)
		}
	}
	if err := ss.setStorageClassChunked(store.StorageClassStandard, toDemote); err != nil {
		logrus.WithError(err).Error("Failed to demote evicted files to storage_class=standard")
		ticker.Reset(intervalException)
		return
	}

	logrus.WithFields(logrus.Fields{
		"hotSet":  len(hotList),
		"demoted": len(toDemote),
	}).Debug("Reconciled storage class")

	ticker.Reset(interval)
}

// setStorageClassChunked updates storage_class for the given root hashes in both
// the submits and address_submits tables, in bounded IN-list chunks.
func (ss *StorageSyncer) setStorageClassChunked(class string, rootHashes []string) error {
	const chunkSize = 1000
	for i := 0; i < len(rootHashes); i += chunkSize {
		end := i + chunkSize
		if end > len(rootHashes) {
			end = len(rootHashes)
		}
		batch := rootHashes[i:end]
		if _, err := ss.db.SubmitStore.SetStorageClassByRootHashes(class, batch); err != nil {
			return err
		}
		if _, err := ss.db.AddressSubmitStore.SetStorageClassByRootHashes(class, batch); err != nil {
			return err
		}
	}
	return nil
}

// checkSyncHeightGaps monitors sync height differences and returns error if gaps exceed 1000 blocks
func (ss *StorageSyncer) checkSyncHeightGaps(nodeSyncHeight uint64) error {
	if ss.blockchainClient == nil {
		return nil
	}

	// Get current blockchain height
	currentBlock, err := ss.blockchainClient.Eth.BlockNumber()
	if err != nil {
		logrus.WithError(err).Error("Failed to get current block height for sync monitoring")
		return err
	}

	currentHeight := currentBlock.Uint64()

	// Get scanner and node sync heights from database
	_, scannerSyncHeight, err := ss.db.GetSyncHeights()
	if err != nil {
		logrus.WithError(err).Error("Failed to get scanner sync height for monitoring")
		return err
	}

	// Accumulate gap errors so we can report all of them at once
	var gapMsgs []string
	// Check layer1-logsyncheight gap (node sync height vs blockchain height)
	if currentHeight > nodeSyncHeight && currentHeight-nodeSyncHeight > ss.storageConfig.SyncGapAlertThreshold {
		gap := currentHeight - nodeSyncHeight
		gapMsgs = append(gapMsgs, fmt.Sprintf("Layer1LogSyncHeight sync gap: %d blocks behind (sync: %d, current: %d)", gap, nodeSyncHeight, currentHeight))
	}

	// Check logsyncheight gap (scanner sync height vs blockchain height)
	if currentHeight > scannerSyncHeight && currentHeight-scannerSyncHeight > ss.storageConfig.SyncGapAlertThreshold {
		gap := currentHeight - scannerSyncHeight
		gapMsgs = append(gapMsgs, fmt.Sprintf("LogSyncHeight sync gap: %d blocks behind (sync: %d, current: %d)", gap, scannerSyncHeight, currentHeight))
	}

	if len(gapMsgs) > 0 {
		return errors.New(strings.Join(gapMsgs, "; "))
	}

	return nil // No sync gap issues
}
