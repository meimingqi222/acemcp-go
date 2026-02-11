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
	// Setup temp dir
	tmpDir, err := os.MkdirTemp("", "indexer_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a file with a long line
	longLine := strings.Repeat("a", 1024)
	content := "line1\n" + longLine + "\nline3"
	filePath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Setup Service
	cfg := &config.Config{
		MaxLineBytes:    512, // Limit to 512 bytes
		MaxLinesPerBlob: 100,
		TextExtensions:  []string{".txt"},
		DataDir:         filepath.Join(tmpDir, "data"),
	}
	logger, _ := logging.NewLogger("error")
	s := New(cfg, logger)

	// Test processFile
	// processFile(projectRoot, absPath, relPath, maxLines)
	blobs := s.processFile(tmpDir, filePath, "test.txt", cfg.MaxLinesPerBlob)

	if len(blobs) != 0 {
		t.Errorf("expected 0 blobs (skipped), got %d", len(blobs))
	}

	// Verify log
	logs := s.opLog.Recent(10)
	found := false
	for _, l := range logs {
		if strings.Contains(l.Message, "skipped test.txt: line 2 too long") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning log about skipped file, got logs: %+v", logs)
	}

	// Test within limit
	cfg.MaxLineBytes = 2048
	blobs = s.processFile(tmpDir, filePath, "test.txt", cfg.MaxLinesPerBlob)
	if len(blobs) == 0 {
		t.Errorf("expected blobs, got 0")
	}
}
