package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/logging"
)

func TestProcessFile_MaxLineBytes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "indexer_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	longLine := strings.Repeat("a", 1024)
	content := "line1\n" + longLine + "\nline3"
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		MaxLineBytes:    512,
		MaxLinesPerBlob: 100,
		TextExtensions:  []string{".txt"},
		DataDir:         filepath.Join(tmpDir, "data"),
	}
	logger, _ := logging.NewLogger("error")
	s := New(cfg, logger)

	blobs := s.processFileWithInfo(tmpDir, filePath, "test.txt", cfg.MaxLinesPerBlob, nil)

	if len(blobs) != 1 {
		t.Fatalf("expected 1 blob, got %d", len(blobs))
	}

	if blobs[0].Truncated != 1 {
		t.Errorf("expected 1 truncated line, got %d", blobs[0].Truncated)
	}

	expectedLine := strings.Repeat("a", 512) + "…"
	if !strings.Contains(blobs[0].Content, expectedLine) {
		t.Errorf("expected truncated line in content")
	}

	cfg.MaxLineBytes = 2048
	blobs = s.processFileWithInfo(tmpDir, filePath, "test.txt", cfg.MaxLinesPerBlob, nil)
	if len(blobs) == 0 {
		t.Errorf("expected blobs, got 0")
	}
	if blobs[0].Truncated != 0 {
		t.Errorf("expected 0 truncated lines, got %d", blobs[0].Truncated)
	}
}
