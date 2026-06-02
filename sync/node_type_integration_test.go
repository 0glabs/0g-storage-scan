//go:build integration

// MySQL-backed integration test for the node-type reconcile worker. Uses the same
// throwaway MySQL as storage_class_integration_test.go (mustTestStore helper).
//
//	docker run -d --name scan-it-mysql -e MYSQL_ROOT_PASSWORD=root \
//	    -e MYSQL_DATABASE=scan_it_test -p 3380:3306 mysql:8
//	go test -tags integration ./sync/ -run TestRefreshNodeTypes -v
package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/health"
)

// fakeIndexer serves JSON-RPC indexer_getShardedNodes returning the given URLs as
// trusted nodes.
func fakeIndexer(urls *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nodes := make([]map[string]string, 0, len(*urls))
		for _, u := range *urls {
			nodes = append(nodes, map[string]string{"url": u})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]interface{}{"trusted": nodes, "discovered": []interface{}{}},
		})
	}))
}

// fakeProviders serves GET /providers returning the given providers in one page.
func fakeProviders(providers *[]rpc.ProviderInfo) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"providers": *providers, "next_cursor": ""})
	}))
}

func nodeTypeMap(t *testing.T, st *store.MysqlStore) map[string]store.StorageNodeType {
	t.Helper()
	nodes, err := st.StorageNodeTypeStore.List(nil)
	if err != nil {
		t.Fatalf("List node types: %v", err)
	}
	m := make(map[string]store.StorageNodeType, len(nodes))
	for _, n := range nodes {
		m[n.Host] = n
	}
	return m
}

func assertNode(t *testing.T, m map[string]store.StorageNodeType, host string, isHot, isRegular bool) {
	t.Helper()
	n, ok := m[host]
	if !ok {
		t.Fatalf("host %s missing from %v", host, m)
	}
	if n.IsHot != isHot || n.IsRegular != isRegular {
		t.Errorf("host %s = {hot:%v regular:%v}, want {hot:%v regular:%v}",
			host, n.IsHot, n.IsRegular, isHot, isRegular)
	}
}

func TestRefreshNodeTypes(t *testing.T) {
	st := mustTestStore(t)
	if err := st.DB.AutoMigrate(&store.StorageNodeType{}); err != nil {
		t.Fatalf("AutoMigrate StorageNodeType: %v", err)
	}
	st.DB.Exec("DELETE FROM storage_node_types")

	regular := []string{"http://a.com:1", "http://b.com:2"}
	hot := []rpc.ProviderInfo{
		{Address: "0xbbb", URL: "http://b.com:9", Active: true},  // b is BOTH
		{Address: "0xccc", URL: "http://c.com:9", Active: true},  // c hot-only
		{Address: "0xddd", URL: "http://d.com:9", Active: false}, // inactive -> ignored
	}
	idx := fakeIndexer(&regular)
	defer idx.Close()
	rtr := fakeProviders(&hot)
	defer rtr.Close()

	ss := MustNewStorageSyncer(st, rpc.StorageConfig{
		Indexer:           idx.URL,
		HotRouter:         rtr.URL,
		NodeTypePageLimit: 100,
		RequestTimeout:    3 * time.Second,
	}, "", health.TimedCounterConfig{}, nil)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	ctx := context.Background()

	// Round 1: a=regular-only, b=both, c=hot-only, d ignored (inactive).
	ss.RefreshNodeTypes(ctx, ticker)
	m := nodeTypeMap(t, st)
	assertNode(t, m, "a.com", false, true)
	assertNode(t, m, "b.com", true, true)
	assertNode(t, m, "c.com", true, false)
	if _, ok := m["d.com"]; ok {
		t.Errorf("inactive provider d.com must not be present")
	}

	// Round 2: b drops from both lists; a regular-only, c hot-only.
	regular = []string{"http://a.com:1"}
	hot = []rpc.ProviderInfo{{Address: "0xccc", URL: "http://c.com:9", Active: true}}
	ss.RefreshNodeTypes(ctx, ticker)
	m = nodeTypeMap(t, st)
	assertNode(t, m, "a.com", false, true)
	assertNode(t, m, "c.com", true, false)
	if _, ok := m["b.com"]; ok {
		t.Errorf("b.com should be demoted from both lists and omitted, got %+v", m["b.com"])
	}

	// Round 3: indexer outage -> regular fetch fails -> must NOT clear regular flags.
	idx.Close()
	ss.RefreshNodeTypes(ctx, ticker)
	m = nodeTypeMap(t, st)
	assertNode(t, m, "a.com", false, true) // still regular despite the outage
	assertNode(t, m, "c.com", true, false) // hot source still healthy
}
