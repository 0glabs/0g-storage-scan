package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/0glabs/0g-storage-client/node"
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/Conflux-Chain/go-conflux-util/health"
	"github.com/Conflux-Chain/go-conflux-util/parallel"
	"github.com/go-resty/resty/v2"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type Status uint8

const (
	NotUploaded Status = iota
	PartialUploaded
	Uploaded
	Pruned
	PrunedCounted
)

var (
	BatchGetSubmitsByGoroutines = 10
)

type FileInfo struct {
	FileInfoParam
	UploadedSegNum uint64
}

type StorageConfig struct {
	Indexer               string
	Retry                 int
	RetryInterval         time.Duration `default:"1s"`
	RequestTimeout        time.Duration `default:"3s"`
	MaxConnsPerHost       int           `default:"1024"`
	AlertChannel          string
	HealthReport          health.TimedCounterConfig
	SyncGapAlertThreshold uint64 `default:"1000"`

	// HotRouter is the base URL of the hot-storage router. When empty, the
	// storage-class reconcile worker is disabled and all files stay "standard".
	HotRouter string
	// ClassReconcileInterval is how often the worker reconciles the hot set.
	ClassReconcileInterval time.Duration `default:"2m"`
	// ClassPageLimit is the page size used when paging GET /files/cached.
	ClassPageLimit int `default:"2000"`
}

type FileInfoParam struct {
	SubmissionIndex uint64
	Status          uint8
}

type FileInfoResult struct {
	Data    *FileInfo
	Err     error
	Latency time.Duration
}

type FileInfoExecutor struct {
	storageConfig StorageConfig
	rpcParams     []FileInfoParam
	rpcResults    map[uint64]*FileInfoResult
}

// ParallelDo implements the parallel.Interface
func (executor *FileInfoExecutor) ParallelDo(ctx context.Context, routine, task int) (*FileInfoResult, error) {
	rpcParam := executor.rpcParams[task]
	var result FileInfoResult
	result.Data, result.Err = executor.getFileInfo(ctx, executor.storageConfig, rpcParam, task)

	return &result, nil
}

// ParallelCollect implements the parallel.Interface
func (executor *FileInfoExecutor) ParallelCollect(ctx context.Context, result *parallel.Result[*FileInfoResult]) error {
	rpcParam := executor.rpcParams[result.Task]
	executor.rpcResults[rpcParam.SubmissionIndex] = result.Value

	return nil
}

// getFileInfo implements the rpcFunc interface
func (executor *FileInfoExecutor) getFileInfo(ctx context.Context, storageConfig StorageConfig,
	rpcParam FileInfoParam, task int) (*FileInfo, error) {
	fileInfo := FileInfo{rpcParam, 0}
	updated := false

	info, err := GetFileInfoByTxSeq(storageConfig, rpcParam.SubmissionIndex)
	if err == nil && info != nil {
		var status uint8
		if info.Pruned {
			status = uint8(Pruned)
		} else if info.Finalized {
			status = uint8(Uploaded)
		} else if info.UploadedSegNum > 0 {
			status = uint8(PartialUploaded)
		}

		if status > fileInfo.Status {
			fileInfo.Status = status
			fileInfo.UploadedSegNum = info.UploadedSegNum
			updated = true
		}
	}

	if !updated {
		return nil, errors.Errorf("Submit %v with status %v not updated", rpcParam.SubmissionIndex, rpcParam.Status)
	}

	return &fileInfo, nil
}

func BatchGetFileInfos(ctx context.Context, storageConfig StorageConfig, rpcParams []FileInfoParam) (
	map[uint64]*FileInfoResult, error) {
	executor := FileInfoExecutor{
		storageConfig: storageConfig,
		rpcParams:     rpcParams,
		rpcResults:    make(map[uint64]*FileInfoResult),
	}

	start := time.Now()
	opt := parallel.SerialOption{Routines: BatchGetSubmitsByGoroutines}
	if err := parallel.Serial(ctx, &executor, len(rpcParams), opt); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	logrus.WithFields(logrus.Fields{
		"files":       len(rpcParams),
		"elapsed(ms)": elapsed,
		"average(ms)": elapsed.Milliseconds() / int64(len(rpcParams)),
	}).Debug("Batch get file info")

	return executor.rpcResults, nil
}

func GetFileInfoByTxSeq(storageConfig StorageConfig, seqNo uint64) (*node.FileInfo, error) {
	url := fmt.Sprintf("%s/file/info/%v", storageConfig.Indexer, seqNo)
	data, err := requestIndexer(url)
	if err != nil {
		return nil, err
	}

	var fileInfo node.FileInfo
	if err = json.Unmarshal(data, &fileInfo); err != nil {
		return nil, errors.Errorf("Failed to unmarshal file info, seqNo %v %s", seqNo, string(data))
	}

	return &fileInfo, nil
}

func GetNodeStatus(storageConfig StorageConfig) (*node.Status, error) {
	url := fmt.Sprintf("%s/node/status", storageConfig.Indexer)
	data, err := requestIndexer(url)
	if err != nil {
		return nil, err
	}

	var status node.Status
	if err = json.Unmarshal(data, &status); err != nil {
		return nil, errors.Errorf("Failed to unmarshal node status, %s", string(data))
	}

	return &status, nil
}

// CachedFilesResponse is the body of the hot router's GET /files/cached.
type CachedFilesResponse struct {
	Hashes     []string `json:"hashes"`
	NextCursor string   `json:"next_cursor"`
}

// ListCachedFiles fetches one page of the hot router's cached-file set (the hot
// set), keyset-paginated by file hash. Pass an empty cursor to start; an empty
// returned nextCursor means there are no more pages. The router returns a plain
// JSON body (not the indexer's BusinessError envelope), so this does not reuse
// requestIndexer.
func ListCachedFiles(cfg StorageConfig, cursor string, limit int) (hashes []string, nextCursor string, err error) {
	url := fmt.Sprintf("%s/files/cached?limit=%d", strings.TrimRight(cfg.HotRouter, "/"), limit)
	if cursor != "" {
		url += "&cursor=" + cursor
	}

	client := resty.New()
	if cfg.RequestTimeout > 0 {
		client.SetTimeout(cfg.RequestTimeout)
	}

	var result CachedFilesResponse
	resp, err := client.R().SetResult(&result).Get(url)
	if err != nil {
		return nil, "", errors.WithMessagef(err, "Failed to request hot router, %s", url)
	}
	if resp.IsError() {
		return nil, "", errors.Errorf("Failed to request hot router, %s status %s body %s",
			url, resp.Status(), resp.String())
	}

	return result.Hashes, result.NextCursor, nil
}

func requestIndexer(url string) ([]byte, error) {
	client := resty.New()
	var result api.BusinessError
	resp, err := client.R().SetResult(&result).Get(url)
	if err != nil {
		return nil, errors.WithMessagef(err, "Failed to request indexer, %s", url)
	}
	if resp.IsError() || result.Code != 0 {
		return nil, errors.Errorf("Failed to request indexer, %s %s", url, resp.String())
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, errors.Errorf("Failed to marshal indexer's response, %s %s", url, resp.String())
	}

	return data, nil
}
