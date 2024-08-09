package stat

import (
	"time"

	"github.com/0glabs/0g-storage-scan/store"
	"github.com/openweb3/web3go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type StatDASigner struct {
	*BaseStat
	statType string
}

func MustNewStatDASigner(cfg *StatConfig, db *store.MysqlStore, sdk *web3go.Client, startTime time.Time) *AbsStat {
	baseStat := &BaseStat{
		Config:    cfg,
		DB:        db,
		Sdk:       sdk,
		StartTime: startTime,
	}

	statDASigner := &StatDASigner{
		BaseStat: baseStat,
		statType: baseStat.Config.MinStatIntervalDailyDASigner,
	}

	return &AbsStat{
		Stat: statDASigner,
		sdk:  baseStat.Sdk,
	}
}

func (sm *StatDASigner) nextTimeRange() (*TimeRange, error) {
	lastStat, err := sm.DB.DASignerStatStore.LastByType(sm.statType)
	if err != nil {
		return nil, err
	}

	var nextRangeStart time.Time
	if lastStat == nil {
		nextRangeStart = sm.StartTime
	} else {
		t := lastStat.StatTime.Add(store.Intervals[sm.statType])
		nextRangeStart = t
	}

	timeRange, err := sm.calStatRange(nextRangeStart, store.Intervals[sm.statType])
	if err != nil {
		return nil, err
	}
	timeRange.intervalType = sm.statType

	return timeRange, nil
}

func (sm *StatDASigner) calculateStat(tr TimeRange) error {
	stat, err := sm.statByTimeRange(tr.start, tr.end, sm.statType)
	if err != nil {
		return err
	}
	dStat, err := sm.statByTimeRange(time.Time{}, tr.end, store.Day)
	if err != nil {
		return err
	}

	stats := []*store.DASignerStat{stat, dStat}
	return sm.DB.DB.Transaction(func(dbTx *gorm.DB) error {
		if err := sm.DB.DASignerStatStore.Del(dbTx, dStat); err != nil {
			return errors.WithMessage(err, "failed to del day stat")
		}
		if err := sm.DB.DASignerStatStore.Add(dbTx, stats); err != nil {
			return errors.WithMessage(err, "failed to save stats")
		}
		return nil
	})
}

func (sm *StatDASigner) statByTimeRange(start, end time.Time, statType string) (*store.DASignerStat, error) {
	// cal range start if not exist
	nilTime := time.Time{}
	if start == nilTime {
		rangeStart, err := sm.calStatRangeStart(end, statType)
		if err != nil {
			return nil, err
		}
		start = rangeStart
	}

	// delta count
	delta, err := sm.DB.DASignerStore.Count(start, end)
	if err != nil {
		return nil, err
	}

	// total count of the previous range
	preTimeRange, err := sm.calStatRange(start, -store.Intervals[statType])
	if err != nil {
		return nil, err
	}

	var daSignerStat store.DASignerStat
	exist, err := sm.DB.MinerStatStore.Exists(&daSignerStat, "stat_type = ? and stat_time = ?",
		statType, preTimeRange.start)
	if err != nil {
		logrus.WithError(err).Error("Failed to query databases")
		return nil, err
	}

	var total uint64
	if !exist {
		count, err := sm.DB.DAClientStore.Count(time.Time{}, start)
		if err != nil {
			return nil, err
		}
		total = count
	} else {
		total = daSignerStat.SignerTotal
	}

	// active count
	//active, err := sm.DB.DASubmitStore.CountActive(start, end)
	//if err != nil {
	//	return nil, err
	//}

	return &store.DASignerStat{
		StatTime:     start,
		StatType:     statType,
		SignerNew:    delta,
		SignerActive: 0,
		SignerTotal:  total + delta,
	}, nil
}
