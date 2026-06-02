package storage

import (
	scanApi "github.com/0glabs/0g-storage-scan/api"
	"github.com/Conflux-Chain/go-conflux-util/api"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// NodeTypeInfo is the per-host hot/regular tag returned by GET /api/storage/node-types.
type NodeTypeInfo struct {
	IsHot        bool   `json:"isHot"`                  // node is a hot-storage provider
	IsRegular    bool   `json:"isRegular"`              // node is a regular storage node
	URL          string `json:"url"`                    // last-seen node url
	ProviderAddr string `json:"providerAddr,omitempty"` // on-chain provider address (hot only)
}

type nodeTypeParam struct {
	NodeType *string `form:"nodeType" binding:"omitempty,oneof=hot regular"`
}

// listNodeTypes returns a map keyed by node host (hostname or IP, port stripped)
// of hot/regular tags, mirroring the indexer's getNodeLocations shape so the
// frontend can merge it by host. Optional ?nodeType=hot|regular filter.
func listNodeTypes(c *gin.Context) (interface{}, error) {
	var param nodeTypeParam
	if err := c.ShouldBind(&param); err != nil {
		return nil, api.ErrValidation(errors.WithMessage(err, "Invalid node-type param"))
	}

	nodes, err := db.StorageNodeTypeStore.List(param.NodeType)
	if err != nil {
		return nil, scanApi.ErrDatabase(errors.WithMessage(err, "Failed to get node types"))
	}

	result := make(map[string]NodeTypeInfo, len(nodes))
	for _, n := range nodes {
		result[n.Host] = NodeTypeInfo{
			IsHot:        n.IsHot,
			IsRegular:    n.IsRegular,
			URL:          n.URL,
			ProviderAddr: n.ProviderAddr,
		}
	}
	return result, nil
}

func listNodeTypesHandler(c *gin.Context) {
	api.Wrap(listNodeTypes)(c)
}
