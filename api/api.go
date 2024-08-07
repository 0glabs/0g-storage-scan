package api

import (
	nhContract "github.com/0glabs/0g-storage-scan/contract"
	"github.com/0glabs/0g-storage-scan/docs"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/api"
	commonApi "github.com/Conflux-Chain/go-conflux-util/api"
	viperUtil "github.com/Conflux-Chain/go-conflux-util/viper"
	"github.com/gin-gonic/gin"
	"github.com/openweb3/web3go"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

const BasePath = "/api"

var (
	sdk         *web3go.Client
	db          *store.MysqlStore
	chargeToken *TokenInfo
)

func MustInit(client *web3go.Client, store *store.MysqlStore) {
	sdk = client
	db = store

	var charge struct {
		Erc20TokenAddress string
		Symbol            string
		Decimals          uint8
	}
	viperUtil.MustUnmarshalKey("charge", &charge)

	if charge.Erc20TokenAddress != "" {
		name, symbol, decimals, err := nhContract.TokenInfo(client, charge.Erc20TokenAddress)
		if err != nil {
			logrus.WithError(err).Fatal("Get erc20 token info")
		}
		chargeToken = &TokenInfo{
			Address:  charge.Erc20TokenAddress,
			Name:     name,
			Symbol:   symbol,
			Decimals: decimals,
		}
	} else {
		chargeToken = &TokenInfo{
			Symbol:   charge.Symbol,
			Decimals: charge.Decimals,
		}
		chargeToken.Native = true
	}

	var flow struct {
		Address              string
		SubmitEventSignature string
	}
	viperUtil.MustUnmarshalKey("flow", &flow)
}

// @title			0G Storage Scan API
// @version		1.0
// @description	Use any http client to fetch data from the 0G Storage Scan.
func init() {
	docs.SwaggerInfo.BasePath = BasePath
}

func RegisterRouter(router *gin.Engine) {
	apiRoute := router.Group(BasePath)

	statsRoute := apiRoute.Group("/stats")
	statsRoute.GET("summary", summaryHandler)
	statsRoute.GET("layer1-tx", listTxStatsHandler)
	statsRoute.GET("storage", listDataStatsHandler)
	statsRoute.GET("fee", listFeeStatsHandler)

	txsRoute := apiRoute.Group("/txs")
	txsRoute.GET("", listTxsHandler)
	txsRoute.GET(":txSeq", getTxHandler)

	rewardsRoute := apiRoute.Group("/rewards")
	rewardsRoute.GET("", listRewardsHandler)

	accountsRoute := apiRoute.Group("/accounts")
	accountsRoute.GET(":address", getAccountInfoHandler)
	accountsRoute.GET(":address/txs", listAddressTxsHandler)
	accountsRoute.GET(":address/rewards", listAddressRewardsHandler)

	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// summaryHandler godoc
//
//	@Summary		Statistics summary
//	@Description	Query statistics summary includes `storage fee` and `log sync height`
//	@Tags			statistic
//	@Produce		json
//	@Success		200	{object}	api.BusinessError{Data=Summary}
//	@Failure		600	{object}	api.BusinessError
//	@Router			/stats/summary [get]
func summaryHandler(c *gin.Context) {
	api.Wrap(summary)(c)
}

// listTxStatsHandler godoc
//
//	@Summary		Layer1 transaction statistics
//	@Description	Query transaction statistics, including incremental and full data, and support querying at hourly or daily time intervals
//	@Tags			statistic
//	@Accept			json
//	@Produce		json
//	@Param			skip			query		int		false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit			query		int		false	"The number of records displayed on the page"								minimum(1)	maximum(2000)	default(10)
//	@Param			minTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			maxTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			intervalType	query		string	false	"Statistics interval"	Enums(hour, day)	default(day)
//	@Param			sort			query		string	false	"Sort by timestamp"		Enums(asc, desc)	default(desc)
//	@Success		200				{object}	api.BusinessError{Data=TxStatList}
//	@Failure		600				{object}	api.BusinessError
//	@Router			/stats/layer1-tx [get]
func listTxStatsHandler(c *gin.Context) {
	api.Wrap(listTxStat)(c)
}

// listDataStatsHandler godoc
//
//	@Summary		Data storage statistics
//	@Description	Query data storage statistics, including incremental and full data, and support querying at hourly or daily time intervals
//	@Tags			statistic
//	@Accept			json
//	@Produce		json
//	@Param			skip			query		int		false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit			query		int		false	"The number of records displayed on the page"								minimum(1)	maximum(2000)	default(10)
//	@Param			minTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			maxTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			intervalType	query		string	false	"Statistics interval"	Enums(hour, day)	default(day)
//	@Param			sort			query		string	false	"Sort by timestamp"		Enums(asc, desc)	default(desc)
//	@Success		200				{object}	api.BusinessError{Data=DataStatList}
//	@Failure		600				{object}	api.BusinessError
//	@Router			/stats/storage [get]
func listDataStatsHandler(c *gin.Context) {
	api.Wrap(listDataStat)(c)
}

// listFeeStatsHandler godoc
//
//	@Summary		Storage fee statistics
//	@Description	Query fee statistics, including incremental and full data, and support querying at hourly or daily time intervals
//	@Tags			statistic
//	@Accept			json
//	@Produce		json
//	@Param			skip			query		int		false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit			query		int		false	"The number of records displayed on the page"								minimum(1)	maximum(2000)	default(10)
//	@Param			minTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			maxTimestamp	query		int		false	"Timestamp in seconds"
//	@Param			intervalType	query		string	false	"Statistics interval"	Enums(hour, day)	default(day)
//	@Param			sort			query		string	false	"Sort by timestamp"		Enums(asc, desc)	default(desc)
//	@Success		200				{object}	api.BusinessError{Data=FeeStatList}
//	@Failure		600				{object}	api.BusinessError
//	@Router			/stats/fee [get]
func listFeeStatsHandler(c *gin.Context) {
	api.Wrap(listFeeStat)(c)
}

// listTxsHandler godoc
//
//	@Summary		Storage transaction list
//	@Description	Query storage transactions
//	@Tags			transaction
//	@Accept			json
//	@Produce		json
//	@Param			skip	query		int	false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit	query		int	false	"The number of records displayed on the page"								minimum(1)	maximum(100)	default(10)
//	@Success		200		{object}	api.BusinessError{Data=StorageTxList}
//	@Failure		600		{object}	api.BusinessError
//	@Router			/txs [get]
func listTxsHandler(c *gin.Context) {
	api.Wrap(listStorageTxs)(c)
}

// getTxHandler godoc
//
//	@Summary		Storage transaction information
//	@Description	Query storage transaction by txSeq
//	@Tags			transaction
//	@Accept			json
//	@Produce		json
//	@Param			txSeq	path		string	true	"storage transaction sequence number"
//	@Success		200		{object}	api.BusinessError{Data=StorageTxDetail}
//	@Failure		600		{object}	api.BusinessError
//	@Router			/txs/{txSeq} [get]
func getTxHandler(c *gin.Context) {
	api.Wrap(getStorageTx)(c)
}

// listRewardsHandler godoc
//
//	@Summary		Storage reward list
//	@Description	Query storage rewards
//	@Tags			reward
//	@Accept			json
//	@Produce		json
//	@Param			skip	query		int	false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit	query		int	false	"The number of records displayed on the page"								minimum(1)	maximum(100)	default(10)
//	@Success		200		{object}	api.BusinessError{Data=RewardList}
//	@Failure		600		{object}	api.BusinessError
//	@Router			/rewards [get]
func listRewardsHandler(c *gin.Context) {
	api.Wrap(listStorageRewards)(c)
}

// getAccountInfoHandler godoc
//
//	@Summary		Account's information
//	@Description	Query account information for specified account
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			address	path		string	false	"The account address"
//	@Success		200		{object}	api.BusinessError{Data=AccountInfo}
//	@Failure		600		{object}	api.BusinessError
//	@Router			/accounts/{address} [get]
func getAccountInfoHandler(c *gin.Context) {
	api.Wrap(getAccountInfo)(c)
}

// listAddressTxsHandler godoc
//
//	@Summary		Account's storage transaction list
//	@Description	Query storage transactions for specified account, support root hash filter
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			address		path		string	false	"The submitter address of the uploaded file"
//	@Param			skip		query		int		false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit		query		int		false	"The number of records displayed on the page"								minimum(1)	maximum(100)	default(10)
//	@Param			rootHash	query		string	false	"The merkle root hash of the uploaded file"
//	@Success		200			{object}	api.BusinessError{Data=StorageTxList}
//	@Failure		600			{object}	api.BusinessError
//	@Router			/accounts/{address}/txs [get]
func listAddressTxsHandler(c *gin.Context) {
	api.Wrap(listAddressStorageTxs)(c)
}

// listAddressRewardsHandler godoc
//
//	@Summary		Account's storage reward list
//	@Description	Query storage rewards for specified account
//	@Tags			account
//	@Accept			json
//	@Produce		json
//	@Param			address	path		string	false	"The submitter address of the uploaded file"
//	@Param			skip	query		int		false	"The number of skipped records, usually it's pageSize * (pageNumber - 1)"	minimum(0)	default(0)
//	@Param			limit	query		int		false	"The number of records displayed on the page"								minimum(1)	maximum(100)	default(10)
//	@Success		200		{object}	api.BusinessError{Data=RewardList}
//	@Failure		600		{object}	api.BusinessError
//	@Router			/accounts/{address}/rewards [get]
func listAddressRewardsHandler(c *gin.Context) {
	api.Wrap(listAddressStorageRewards)(c)
}

func getAddressInfo(c *gin.Context) (*AddressInfo, error) {
	address := c.Param("address")
	if address == "" {
		logrus.Error("Failed to parse nil address")
		return nil, errors.Errorf("Biz error, nil address %v", address)
	}

	addressInfo, exist, err := db.AddressStore.Get(address)
	if err != nil {
		return nil, commonApi.ErrInternal(err)
	}
	if !exist {
		return nil, ErrAddressNotFound
	}

	return &AddressInfo{address, addressInfo.ID}, nil
}
