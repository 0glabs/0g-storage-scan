package sync

import (
	"context"
	"errors"
	"fmt"
	"net/url"
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

// RefreshNodeTypes tags storage nodes as hot and/or regular by reconciling against
// two independent sources: the regular indexer's node list (indexer_getShardedNodes)
// and the hot router's provider list (GET /providers). A single host can be both.
// Each source is reconciled independently and is NEVER cleared on its own fetch
// failure (outage safety). These lists change rarely, so it runs daily.
func (ss *StorageSyncer) RefreshNodeTypes(ctx context.Context, ticker *time.Ticker) {
	if interrupted(ctx) {
		return
	}

	cfg := ss.storageConfig
	if cfg.Indexer == "" && cfg.HotRouter == "" {
		ticker.Reset(time.Hour) // nothing to tag
		return
	}

	interval := cfg.NodeTypeRefreshInterval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	limit := cfg.NodeTypePageLimit
	if limit <= 0 {
		limit = 2000
	}

	failed := false

	// Regular source: indexer node list. Skip the reconcile (no clearing) on a fetch error.
	if cfg.Indexer != "" {
		if urls, err := rpc.GetShardedNodes(cfg); err != nil {
			logrus.WithError(err).Warn("Failed to fetch regular node list from indexer; skipping regular node-type reconcile")
			failed = true
		} else if err := ss.reconcileRegularNodes(urls); err != nil {
			logrus.WithError(err).Error("Failed to reconcile regular node types")
			failed = true
		}
	}

	// Hot source: router provider list. Skip the reconcile (no clearing) on a fetch error.
	if cfg.HotRouter != "" {
		if providers, err := ss.fetchHotProviders(cfg, limit); err != nil {
			logrus.WithError(err).Warn("Failed to fetch hot providers from router; skipping hot node-type reconcile")
			failed = true
		} else if err := ss.reconcileHotNodes(providers); err != nil {
			logrus.WithError(err).Error("Failed to reconcile hot node types")
			failed = true
		}
	}

	if failed {
		ticker.Reset(intervalException)
		return
	}
	ticker.Reset(interval)
}

func (ss *StorageSyncer) fetchHotProviders(cfg rpc.StorageConfig, limit int) ([]rpc.ProviderInfo, error) {
	var all []rpc.ProviderInfo
	cursor := ""
	for {
		page, next, err := rpc.ListProviders(cfg, cursor, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}

func (ss *StorageSyncer) reconcileRegularNodes(urls []string) error {
	present := make(map[string]string, len(urls)) // host -> representative url
	for _, u := range urls {
		if h := hostFromURL(u); h != "" {
			present[h] = u
		}
	}

	nodes := make([]store.StorageNodeType, 0, len(present))
	for h, u := range present {
		nodes = append(nodes, store.StorageNodeType{Host: h, URL: u})
	}
	if err := ss.db.StorageNodeTypeStore.UpsertRegular(nodes); err != nil {
		return err
	}

	current, err := ss.db.StorageNodeTypeStore.RegularHosts()
	if err != nil {
		return err
	}
	toClear := make([]string, 0)
	for _, h := range current {
		if _, ok := present[h]; !ok {
			toClear = append(toClear, h)
		}
	}
	return ss.db.StorageNodeTypeStore.ClearRegular(toClear)
}

func (ss *StorageSyncer) reconcileHotNodes(providers []rpc.ProviderInfo) error {
	present := make(map[string]store.StorageNodeType, len(providers)) // host -> node
	for _, p := range providers {
		if !p.Active {
			continue // inactive providers are not hot
		}
		if h := hostFromURL(p.URL); h != "" {
			present[h] = store.StorageNodeType{Host: h, URL: p.URL, ProviderAddr: p.Address}
		}
	}

	nodes := make([]store.StorageNodeType, 0, len(present))
	for _, n := range present {
		nodes = append(nodes, n)
	}
	if err := ss.db.StorageNodeTypeStore.UpsertHot(nodes); err != nil {
		return err
	}

	current, err := ss.db.StorageNodeTypeStore.HotHosts()
	if err != nil {
		return err
	}
	toClear := make([]string, 0)
	for _, h := range current {
		if _, ok := present[h]; !ok {
			toClear = append(toClear, h)
		}
	}
	return ss.db.StorageNodeTypeStore.ClearHot(toClear)
}

// hostFromURL extracts the host (hostname or IP, port stripped) from a node URL,
// matching the frontend's extractIp (URL.hostname). Falls back to scheme-less
// "host:port" parsing, then to the raw string.
func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	if u, err := url.Parse("//" + raw); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return raw
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
