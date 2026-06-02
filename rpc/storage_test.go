package rpc

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestListCachedFiles_PagesAndParsesParams(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			io.WriteString(w, `{"hashes":["0x01","0x02"],"next_cursor":"0x02"}`)
			return
		}
		io.WriteString(w, `{"hashes":["0x03"],"next_cursor":""}`)
	}))
	defer srv.Close()

	cfg := StorageConfig{HotRouter: srv.URL}

	hashes, next, err := ListCachedFiles(cfg, "", 2)
	if err != nil {
		t.Fatalf("page1: unexpected error: %v", err)
	}
	if next != "0x02" {
		t.Fatalf("page1 nextCursor = %q, want 0x02", next)
	}
	if len(hashes) != 2 || hashes[0] != "0x01" || hashes[1] != "0x02" {
		t.Fatalf("page1 hashes = %v, want [0x01 0x02]", hashes)
	}

	hashes2, next2, err := ListCachedFiles(cfg, next, 2)
	if err != nil {
		t.Fatalf("page2: unexpected error: %v", err)
	}
	if next2 != "" {
		t.Fatalf("page2 nextCursor = %q, want empty", next2)
	}
	if len(hashes2) != 1 || hashes2[0] != "0x03" {
		t.Fatalf("page2 hashes = %v, want [0x03]", hashes2)
	}

	// Verify outgoing query params: limit on both pages, cursor only on the second.
	if len(gotPaths) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(gotPaths))
	}
	q0, _ := url.Parse(gotPaths[0])
	if got := q0.Query().Get("limit"); got != "2" {
		t.Errorf("page1 limit = %q, want 2", got)
	}
	if _, ok := q0.Query()["cursor"]; ok {
		t.Errorf("page1 should not send a cursor, got path %q", gotPaths[0])
	}
	q1, _ := url.Parse(gotPaths[1])
	if got := q1.Query().Get("cursor"); got != "0x02" {
		t.Errorf("page2 cursor = %q, want 0x02", got)
	}
}

func TestListCachedFiles_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, _, err := ListCachedFiles(StorageConfig{HotRouter: srv.URL}, "", 10); err == nil {
		t.Fatal("expected an error on a 500 response, got nil")
	}
}

func TestGetShardedNodes_ParsesTrustedAndDiscovered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method != "indexer_getShardedNodes" {
			t.Errorf("rpc method = %q, want indexer_getShardedNodes", req.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"trusted":[{"url":"http://1.1.1.1:1"}],"discovered":[{"url":"http://2.2.2.2:2"}]}}`)
	}))
	defer srv.Close()

	urls, err := GetShardedNodes(StorageConfig{Indexer: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(urls) != 2 || urls[0] != "http://1.1.1.1:1" || urls[1] != "http://2.2.2.2:2" {
		t.Fatalf("urls = %v, want [trusted discovered]", urls)
	}
}

func TestGetShardedNodes_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"boom"}}`)
	}))
	defer srv.Close()

	if _, err := GetShardedNodes(StorageConfig{Indexer: srv.URL}); err == nil {
		t.Fatal("expected an error on a JSON-RPC error response, got nil")
	}
}

func TestListProviders_PagesAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			io.WriteString(w, `{"providers":[{"address":"0xabc","url":"http://h1:1","active":true}],"next_cursor":"0xabc"}`)
			return
		}
		io.WriteString(w, `{"providers":[{"address":"0xdef","url":"http://h2:2","active":false}],"next_cursor":""}`)
	}))
	defer srv.Close()

	cfg := StorageConfig{HotRouter: srv.URL}

	p1, next, err := ListProviders(cfg, "", 1)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if next != "0xabc" || len(p1) != 1 || p1[0].Address != "0xabc" || p1[0].URL != "http://h1:1" || !p1[0].Active {
		t.Fatalf("page1 = %+v next=%s", p1, next)
	}

	p2, next2, err := ListProviders(cfg, next, 1)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if next2 != "" || len(p2) != 1 || p2[0].Address != "0xdef" || p2[0].Active {
		t.Fatalf("page2 = %+v next=%s", p2, next2)
	}
}
