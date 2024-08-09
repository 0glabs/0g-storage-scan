package stat

import (
	"context"
	"sync"
	"time"

	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/viper"
	"github.com/openweb3/web3go"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	ErrTimeNotReach      = errors.New("time not reach")
	ErrBlockNotSync      = errors.New("block not sync")
	ErrBlockNotFinalized = errors.New("block not finalized")
)

type StatConfig struct {
	BlockOnStatBegin             uint64
	MinStatIntervalDailyTx       string `default:"10m"`
	MinStatIntervalDailySubmit   string `default:"10m"`
	MinStatIntervalDailyAddress  string `default:"10m"`
	MinStatIntervalDailyMiner    string `default:"1h"`
	MinStatIntervalDailyDASubmit string `default:"10m"`
	MinStatIntervalDailyDAClient string `default:"10m"`
	MinStatIntervalDailyDASigner string `default:"10m"`
}

type TimeRange struct {
	start        time.Time
	end          time.Time
	intervalType string
}

type BaseStat struct {
	Config    *StatConfig
	DB        *store.MysqlStore
	Sdk       *web3go.Client
	StartTime time.Time
}

func (bs *BaseStat) calStatRange(rangeStart time.Time, interval time.Duration) (*TimeRange, error) {
	rangeEnd := rangeStart.Add(interval)

	var timeRange TimeRange
	if interval < 0 {
		timeRange = TimeRange{
			start: rangeEnd,
			end:   rangeStart,
		}
	} else {
		timeRange = TimeRange{
			start: rangeStart,
			end:   rangeEnd,
		}
	}

	return &timeRange, nil
}

/*
calStatRangeStart calculate the start time of range in specified range type, and the range type includes Month Day and Hour
Examples:
Range in Month
current time "2023-02-01 00:00:00", expect range start time "2023-01-01 00:00:00"
current time "2023-01-01 00:00:00", expect range start time "2022-12-01 00:00:00"
Range in Day
current time "2023-01-02 00:00:00", expect range start time "2023-01-01 00:00:00"
current time "2023-01-01 00:00:00", expect range start time "2022-12-31 00:00:00"
Range in Hour
current time "2023-01-01 01:00:00", expect range start time "2023-01-01 00:00:00"
current time "2023-01-01 00:00:00", expect range start time "2022-12-31 23:00:00"
*/
func (bs *BaseStat) calStatRangeStart(t time.Time, statType string) (time.Time, error) {
	var rangeStart time.Time
	timeFormat := t.Format("2006-01-02 15:04:05")

	switch statType {
	case "1M":
		rangeStart = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
		if timeFormat[8:19] == "01 00:00:00" {
			rangeStart = rangeStart.AddDate(0, -1, 0)
		}
	case "1d":
		rangeStart = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
		if timeFormat[11:19] == "00:00:00" {
			rangeStart = rangeStart.AddDate(0, 0, -1)
		}
	case "1h":
		rangeStart = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
		if timeFormat[14:19] == "00:00" {
			rangeStart = rangeStart.Add(-time.Hour)
		}
	default:
		return time.Time{}, errors.Errorf("stat type %v not supported", statType)
	}

	return rangeStart, nil
}

func (bs *BaseStat) firstBlockAfterRangeEnd(rangeEnd time.Time) (uint64, bool, error) {
	return bs.DB.FirstBlockAfterTime(rangeEnd)
}

type Stat interface {
	nextTimeRange() (*TimeRange, error)
	firstBlockAfterRangeEnd(rangeEnd time.Time) (uint64, bool, error)
	calculateStat(TimeRange) error
}

type AbsStat struct {
	sdk *web3go.Client
	Stat
}

func (as *AbsStat) DoStat(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	defer wg.Done()
	logrus.Info("Stat starting")

	for {
		timeRange, err := as.tryAcquireTimeRange()

		if as.interrupted(ctx) {
			break
		}

		if err != nil {
			if !errors.Is(err, ErrTimeNotReach) &&
				!errors.Is(err, ErrBlockNotSync) &&
				!errors.Is(err, ErrBlockNotFinalized) {
				logrus.WithError(err).WithField("timeRange", timeRange).
					Warn("acquire next time range for stat txs")
			}
			time.Sleep(time.Second)
			continue
		}

		err = as.calculateStat(*timeRange)
		if err != nil {
			logrus.WithError(err).Error("do stat")
			time.Sleep(time.Second * 10)
			continue
		}
	}
}

func (as *AbsStat) tryAcquireTimeRange() (*TimeRange, error) {
	timeRange, err := as.nextTimeRange()
	if err != nil {
		return nil, err
	}
	if time.Now().UTC().Before(timeRange.end) {
		return nil, ErrTimeNotReach
	}

	blockNum, exists, err := as.firstBlockAfterRangeEnd(timeRange.end)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrBlockNotSync
	}

	blockFinalized, err := as.sdk.Eth.BlockByNumber(types.FinalizedBlockNumber, false)
	if err != nil {
		return nil, err
	}
	if blockNum > blockFinalized.Number.Uint64() {
		return nil, ErrBlockNotFinalized
	}

	return timeRange, nil
}

func (as *AbsStat) interrupted(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}

func MustDefaultRangeStart(sdk *web3go.Client) time.Time {
	start, err := defaultRangeStart(sdk)
	if err != nil {
		logrus.WithError(err).Fatal("Failed to get default start time for stat task")
	}

	return start
}

func defaultRangeStart(sdk *web3go.Client) (time.Time, error) {
	config := StatConfig{}
	viper.MustUnmarshalKey("stat", &config)

	if config.BlockOnStatBegin == uint64(0) {
		return time.Time{}, errors.New("missing block from which the stat begin")
	}

	block, err := sdk.Eth.BlockByNumber(types.BlockNumber(config.BlockOnStatBegin), false)
	if err != nil {
		return time.Time{}, err
	}

	t := time.Unix(int64(block.Timestamp), 0).UTC()
	rangeStart := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())

	return rangeStart, nil
}
