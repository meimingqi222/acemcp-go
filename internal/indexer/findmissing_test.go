package indexer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/logging"
)

func TestFindMissing_BatchedMergesResults(t *testing.T) {
	var calls int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find-missing" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing token"}`))
			return
		}
		atomic.AddInt64(&calls, 1)

		var in struct {
			Names []string `json:"mem_object_names"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad json"}`))
			return
		}

		var out findMissingResult
		for _, n := range in.Names {
			if len(n) > 0 && n[len(n)-1] == 'u' {
				out.UnknownBlobNames = append(out.UnknownBlobNames, n)
				continue
			}
			if len(n) > 0 && n[len(n)-1] == 'n' {
				out.NonindexedBlobNames = append(out.NonindexedBlobNames, n)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	tmpDir, err := os.MkdirTemp("", "findmissing_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		BaseURL:        srv.URL,
		Token:          "t",
		DataDir:        filepath.Join(tmpDir, "data"),
		MaxLineBytes:   10 * 1024,
		TextExtensions: []string{".go"},
	}
	logger, _ := logging.NewLogger("error")
	s := New(cfg, logger)
	s.client = srv.Client()
	s.client.Timeout = 5 * time.Second

	blobNames := make([]string, 0, 1200)
	wantUnknown := make(map[string]struct{})
	wantNonindexed := make(map[string]struct{})
	for i := 0; i < 1200; i++ {
		name := "blob_" + strconv.Itoa(i)
		switch {
		case i%10 == 0:
			name += "u"
			wantUnknown[name] = struct{}{}
		case i%10 == 1:
			name += "n"
			wantNonindexed[name] = struct{}{}
		}
		blobNames = append(blobNames, name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := s.findMissing(ctx, blobNames)
	if err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("expected 3 requests, got %d", got)
	}

	gotUnknown := make(map[string]struct{}, len(res.UnknownBlobNames))
	for _, n := range res.UnknownBlobNames {
		gotUnknown[n] = struct{}{}
	}
	gotNonindexed := make(map[string]struct{}, len(res.NonindexedBlobNames))
	for _, n := range res.NonindexedBlobNames {
		gotNonindexed[n] = struct{}{}
	}

	if len(gotUnknown) != len(wantUnknown) {
		t.Fatalf("unknown size mismatch: want %d got %d", len(wantUnknown), len(gotUnknown))
	}
	for n := range wantUnknown {
		if _, ok := gotUnknown[n]; !ok {
			t.Fatalf("missing unknown name %q", n)
		}
	}
	if len(gotNonindexed) != len(wantNonindexed) {
		t.Fatalf("nonindexed size mismatch: want %d got %d", len(wantNonindexed), len(gotNonindexed))
	}
	for n := range wantNonindexed {
		if _, ok := gotNonindexed[n]; !ok {
			t.Fatalf("missing nonindexed name %q", n)
		}
	}
}

func TestFindMissing_HTTPErrorIncludesOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/find-missing" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("x-request-id", "req-1")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"Unidentified internal error"}`))
	}))
	defer srv.Close()

	tmpDir, err := os.MkdirTemp("", "findmissing_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		BaseURL:        srv.URL,
		Token:          "t",
		DataDir:        filepath.Join(tmpDir, "data"),
		MaxLineBytes:   10 * 1024,
		TextExtensions: []string{".go"},
	}
	logger, _ := logging.NewLogger("error")
	s := New(cfg, logger)
	s.client = srv.Client()
	s.client.Timeout = 5 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = s.findMissing(ctx, []string{"a"})
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*apiHTTPError)
	if !ok {
		t.Fatalf("expected *apiHTTPError, got %T", err)
	}
	if he.Operation != "find-missing" {
		t.Fatalf("expected operation find-missing, got %q", he.Operation)
	}
	if he.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", he.StatusCode)
	}
	if he.RequestID != "req-1" {
		t.Fatalf("expected request id req-1, got %q", he.RequestID)
	}
}
