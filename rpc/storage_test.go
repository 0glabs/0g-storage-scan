package rpc

import (
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
