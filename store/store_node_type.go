package store

import (
	"time"

	"github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StorageNodeType records whether a storage node (identified by its URL host —
// hostname or IP, port stripped) is a hot-storage provider, a regular storage
// node, or both. It is a tag overlay maintained by the RefreshNodeTypes worker;
// the frontend merges it onto its live node list by host.
type StorageNodeType struct {
	Host         string    `gorm:"column:host;size:255;primaryKey"`
	IsHot        bool      `gorm:"not null;default:false;index"`
	IsRegular    bool      `gorm:"not null;default:false;index"`
	URL          string    `gorm:"size:255"` // last-seen full url, for display
	ProviderAddr string    `gorm:"size:42"`  // on-chain addr, only for hot nodes
	UpdatedAt    time.Time `gorm:"autoUpdateTime"`
}

func (StorageNodeType) TableName() string {
	return "storage_node_types"
}

type StorageNodeTypeStore struct {
	*mysql.Store
}

func newStorageNodeTypeStore(db *gorm.DB) *StorageNodeTypeStore {
	return &StorageNodeTypeStore{
		Store: mysql.NewStore(db),
	}
}

// UpsertRegular marks the given hosts as regular storage nodes, creating rows as
// needed and preserving any existing is_hot flag. is_hot/provider_addr are left
// untouched so the two sources stay independent.
func (s *StorageNodeTypeStore) UpsertRegular(nodes []StorageNodeType) error {
	if len(nodes) == 0 {
		return nil
	}
	for i := range nodes {
		nodes[i].IsRegular = true
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "host"}},
		DoUpdates: clause.AssignmentColumns([]string{"is_regular", "url", "updated_at"}),
	}).CreateInBatches(nodes, batchSizeInsert).Error
}

// UpsertHot marks the given hosts as hot-storage providers, creating rows as needed
// and preserving any existing is_regular flag.
func (s *StorageNodeTypeStore) UpsertHot(nodes []StorageNodeType) error {
	if len(nodes) == 0 {
		return nil
	}
	for i := range nodes {
		nodes[i].IsHot = true
	}
	return s.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "host"}},
		DoUpdates: clause.AssignmentColumns([]string{"is_hot", "url", "provider_addr", "updated_at"}),
	}).CreateInBatches(nodes, batchSizeInsert).Error
}

// HotHosts returns the hosts currently flagged hot (for computing demotions).
func (s *StorageNodeTypeStore) HotHosts() ([]string, error) {
	var hosts []string
	err := s.DB.Model(&StorageNodeType{}).Where("is_hot = ?", true).Pluck("host", &hosts).Error
	return hosts, err
}

// RegularHosts returns the hosts currently flagged regular (for computing demotions).
func (s *StorageNodeTypeStore) RegularHosts() ([]string, error) {
	var hosts []string
	err := s.DB.Model(&StorageNodeType{}).Where("is_regular = ?", true).Pluck("host", &hosts).Error
	return hosts, err
}

// ClearHot sets is_hot=false for the given hosts (evicted/removed hot providers).
func (s *StorageNodeTypeStore) ClearHot(hosts []string) error {
	if len(hosts) == 0 {
		return nil
	}
	return s.DB.Model(&StorageNodeType{}).Where("host IN ?", hosts).Update("is_hot", false).Error
}

// ClearRegular sets is_regular=false for the given hosts.
func (s *StorageNodeTypeStore) ClearRegular(hosts []string) error {
	if len(hosts) == 0 {
		return nil
	}
	return s.DB.Model(&StorageNodeType{}).Where("host IN ?", hosts).Update("is_regular", false).Error
}

// List returns node-type rows that are hot and/or regular (rows with both flags
// false are omitted). nodeType filters to "hot" or "regular" when non-nil.
func (s *StorageNodeTypeStore) List(nodeType *string) ([]StorageNodeType, error) {
	db := s.DB.Model(&StorageNodeType{})
	switch {
	case nodeType != nil && *nodeType == "hot":
		db = db.Where("is_hot = ?", true)
	case nodeType != nil && *nodeType == "regular":
		db = db.Where("is_regular = ?", true)
	default:
		db = db.Where("is_hot = ? OR is_regular = ?", true, true)
	}

	var nodes []StorageNodeType
	if err := db.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}
