package api

import (
	"github.com/gin-gonic/gin"
)

func listDADataStat(c *gin.Context) (interface{}, error) {
	var statP statParam
	if err := c.ShouldBind(&statP); err != nil {
		return nil, err
	}

	total, records, err := db.DASubmitStatStore.List(&statP.IntervalType, statP.MinTimestamp, statP.MaxTimestamp,
		statP.isDesc(), statP.Skip, statP.Limit)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	result["total"] = total

	list := make([]DADataStat, 0)
	for _, r := range records {
		list = append(list, DADataStat{
			StatTime:        r.StatTime,
			BlobNew:         r.BlobNew,
			BlobTotal:       r.BlobTotal,
			DataSizeNew:     r.DataSizeNew,
			DataSizeTotal:   r.DataSizeTotal,
			StorageFeeNew:   r.StorageFeeNew,
			StorageFeeTotal: r.StorageFeeTotal,
		})
	}
	result["list"] = list

	return result, nil
}

func listDAClientStat(c *gin.Context) (interface{}, error) {
	var statP statParam
	if err := c.ShouldBind(&statP); err != nil {
		return nil, err
	}

	total, records, err := db.DAClientStatStore.List(&statP.IntervalType, statP.MinTimestamp, statP.MaxTimestamp,
		statP.isDesc(), statP.Skip, statP.Limit)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	result["total"] = total

	list := make([]DAClientStat, 0)
	for _, r := range records {
		list = append(list, DAClientStat{
			StatTime:     r.StatTime,
			ClientNew:    r.ClientNew,
			ClientActive: r.ClientActive,
			ClientTotal:  r.ClientTotal,
		})
	}
	result["list"] = list

	return result, nil
}

func listDASignerStat(c *gin.Context) (interface{}, error) {
	var statP statParam
	if err := c.ShouldBind(&statP); err != nil {
		return nil, err
	}

	total, records, err := db.DASignerStatStore.List(&statP.IntervalType, statP.MinTimestamp, statP.MaxTimestamp,
		statP.isDesc(), statP.Skip, statP.Limit)
	if err != nil {
		return nil, err
	}

	result := make(map[string]interface{})
	result["total"] = total

	list := make([]DASignerStat, 0)
	for _, r := range records {
		list = append(list, DASignerStat{
			StatTime:     r.StatTime,
			SignerNew:    r.SignerNew,
			SignerActive: r.SignerActive,
			SignerTotal:  r.SignerTotal,
		})
	}
	result["list"] = list

	return result, nil
}
