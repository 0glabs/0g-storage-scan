//go:build integration

// MySQL-backed integration test for the storage-class reconcile worker.
//
// Run against a throwaway MySQL (defaults match the docker container started by
// the test runner):
//
//	docker run -d --name scan-it-mysql -e MYSQL_ROOT_PASSWORD=root \
//	    -e MYSQL_DATABASE=scan_it_test -p 3380:3306 mysql:8
//	go test -tags integration ./sync/ -run TestReconcileStorageClass -v
//
// Override via MYSQL_HOST / MYSQL_USER / MYSQL_PASSWORD / MYSQL_DATABASE.
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0glabs/0g-storage-scan/rpc"
	"github.com/0glabs/0g-storage-scan/store"
	"github.com/Conflux-Chain/go-conflux-util/health"
	cfxmysql "github.com/Conflux-Chain/go-conflux-util/store/mysql"
	"github.com/shopspring/decimal"
)

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustTestStore(t *testing.T) *store.MysqlStore {
	t.Helper()
	cfg := cfxmysql.Config{
		Host:            envOr("MYSQL_HOST", "127.0.0.1:3380"),
		Username:        envOr("MYSQL_USER", "root"),
		Password:        envOr("MYSQL_PASSWORD", "root"),
		Database:        envOr("MYSQL_DATABASE", "scan_it_test"),
		ConnMaxLifetime: 3 * time.Minute,
		MaxOpenConns:    10,
		MaxIdleConns:    10,
		LogLevel:        "error",
		SlowThreshold:   200 * time.Millisecond,
	}
	db := cfg.MustOpenOrCreate(&store.Submit{}, &store.AddressSubmit{})
	// Exercise the production migration path (AutoMigrate adds the storage_class
	// column to a pre-existing table) and start from a clean slate.
	if err := db.AutoMigrate(&store.Submit{}, &store.AddressSubmit{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	db.Exec("DELETE FROM submits")
	db.Exec("DELETE FROM address_submits")
	return store.MustNewStore(db, cfg)
}

func seedFile(t *testing.T, st *store.MysqlStore, idx uint64, root string) {
	t.Helper()
	txHash := fmt.Sprintf("0x%064x", idx)
	now := time.Now()
	// StorageClass intentionally left unset to verify the column default applies.
	if err := st.DB.Create(&store.Submit{
		SubmissionIndex: idx, RootHash: root, SenderID: 1, Length: 100,
		BlockNumber: idx, BlockTime: now, TxHash: txHash, Fee: decimal.Zero,
	}).Error; err != nil {
		t.Fatalf("seed submit %d: %v", idx, err)
	}
	if err := st.DB.Create(&store.AddressSubmit{
		SenderID: 1, SubmissionIndex: idx, RootHash: root, Length: 100,
		BlockNumber: idx, BlockTime: now, TxHash: txHash, Fee: decimal.Zero,
	}).Error; err != nil {
		t.Fatalf("seed address_submit %d: %v", idx, err)
	}
}

func classOf(t *testing.T, st *store.MysqlStore, idx uint64) (string, string) {
	t.Helper()
	var s store.Submit
	if err := st.DB.First(&s, "submission_index = ?", idx).Error; err != nil {
		t.Fatalf("read submit %d: %v", idx, err)
	}
	var as store.AddressSubmit
	if err := st.DB.First(&as, "submission_index = ?", idx).Error; err != nil {
		t.Fatalf("read address_submit %d: %v", idx, err)
	}
	return s.StorageClass, as.StorageClass
}

// fakeRouter serves GET /files/cached with real keyset pagination over the given
// hot set, mirroring the production router so the worker's paging loop is exercised.
func fakeRouter(hot *[]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		set := append([]string(nil), (*hot)...)
		sort.Strings(set)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = 2000
		}
		cursor := r.URL.Query().Get("cursor")
		out := make([]string, 0, limit)
		for _, h := range set {
			if cursor == "" || h > cursor {
				out = append(out, h)
				if len(out) == limit {
					break
				}
			}
		}
		next := ""
		if len(out) == limit {
			next = out[len(out)-1]
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"hashes": out, "next_cursor": next})
	}))
}

func TestReconcileStorageClass(t *testing.T) {
	st := mustTestStore(t)

	rootA := "0x" + strings.Repeat("a", 64) // will be hot
	rootB := "0x" + strings.Repeat("b", 64) // stays standard
	rootC := "0x" + strings.Repeat("c", 64) // hot, then evicted
	seedFile(t, st, 1, rootA)
	seedFile(t, st, 2, rootB)
	seedFile(t, st, 3, rootC)

	// Newly-inserted rows must default to standard (column default).
	if c, ac := classOf(t, st, 1); c != "standard" || ac != "standard" {
		t.Fatalf("default class = (%q,%q), want (standard,standard)", c, ac)
	}

	hot := []string{rootA, rootC}
	srv := fakeRouter(&hot)
	defer srv.Close()

	// ClassPageLimit=1 forces multi-page keyset pagination through the worker loop.
	ss := MustNewStorageSyncer(st, rpc.StorageConfig{
		HotRouter:      srv.URL,
		ClassPageLimit: 1,
		RequestTimeout: 3 * time.Second,
	}, "", health.TimedCounterConfig{}, nil)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	ctx := context.Background()

	// First reconcile: A and C become hot, B stays standard — in both tables.
	ss.ReconcileStorageClass(ctx, ticker)
	assertClass(t, st, 1, "hot")
	assertClass(t, st, 2, "standard")
	assertClass(t, st, 3, "hot")

	// Evict C from the hot set; next reconcile must demote C, keep A hot.
	hot = []string{rootA}
	ss.ReconcileStorageClass(ctx, ticker)
	assertClass(t, st, 1, "hot")
	assertClass(t, st, 2, "standard")
	assertClass(t, st, 3, "standard")
}

func assertClass(t *testing.T, st *store.MysqlStore, idx uint64, want string) {
	t.Helper()
	c, ac := classOf(t, st, idx)
	if c != want {
		t.Errorf("submit %d storage_class = %q, want %q", idx, c, want)
	}
	if ac != want {
		t.Errorf("address_submit %d storage_class = %q, want %q (My Files must stay consistent)", idx, ac, want)
	}
}

// TestReconcileStorageClass_RouterDownNoDemote verifies a router outage never
// mass-flips already-hot files back to standard.
func TestReconcileStorageClass_RouterDownNoDemote(t *testing.T) {
	st := mustTestStore(t)
	rootA := "0x" + strings.Repeat("a", 64)
	seedFile(t, st, 1, rootA)

	hot := []string{rootA}
	srv := fakeRouter(&hot)
	ss := MustNewStorageSyncer(st, rpc.StorageConfig{HotRouter: srv.URL, ClassPageLimit: 10}, "",
		health.TimedCounterConfig{}, nil)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	ctx := context.Background()

	ss.ReconcileStorageClass(ctx, ticker)
	assertClass(t, st, 1, "hot")

	// Router goes down: point at a closed server. The hot file must remain hot.
	srv.Close()
	ss.ReconcileStorageClass(ctx, ticker)
	assertClass(t, st, 1, "hot")
}
