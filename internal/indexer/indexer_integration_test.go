package indexer

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/logging"
)

func TestSearchContextWithFindMissing(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Token == "" {
		t.Fatal("TOKEN not set in ~/.acemcp/settings.toml")
	}

	cfg.DataDir = t.TempDir()

	logger, err := logging.NewLogger("debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	svc := New(cfg, logger)

	projectRoot := "D:/code/code-context"

	t.Run("FirstIndexAndSearch", func(t *testing.T) {
		start := time.Now()

		result, err := svc.SearchContext(projectRoot, "how does the search tool work")
		if err != nil {
			t.Fatalf("SearchContext failed: %v", err)
		}

		elapsed := time.Since(start)
		t.Logf("Search completed in %v", elapsed)
		t.Logf("Result status: %s", result.Status)
		t.Logf("Result length: %d chars", len(result.Output))

		if result.Output == "" || result.Output == "No relevant code context found." {
			t.Error("Expected non-empty search result on first search")
		}

		if len(result.Output) < 100 {
			t.Errorf("Result seems too short: %d chars", len(result.Output))
		}

		t.Logf("First 500 chars of result:\n%s", truncateForLog(result.Output, 500))
	})

	t.Run("SecondSearchShouldBeFast", func(t *testing.T) {
		start := time.Now()

		result, err := svc.SearchContext(projectRoot, "explain the codebase structure")
		if err != nil {
			t.Fatalf("SearchContext failed: %v", err)
		}

		elapsed := time.Since(start)
		t.Logf("Second search completed in %v", elapsed)

		if elapsed > 30*time.Second {
			t.Errorf("Second search took too long: %v (should be fast since already indexed)", elapsed)
		}

		if result.Output == "" || result.Output == "No relevant code context found." {
			t.Error("Expected non-empty search result")
		}

		t.Logf("Result length: %d chars", len(result.Output))
	})

	t.Run("CheckOpLogs", func(t *testing.T) {
		logs := svc.RecentLogs(50)
		t.Logf("Recent operation logs (%d entries):", len(logs))
		for _, log := range logs {
			t.Logf("  [%s] %s: %s", log.Level, log.Type, log.Message)
		}

		var foundFindMissing bool
		for _, log := range logs {
			if contains(log.Message, "waiting for") && contains(log.Message, "blobs to be indexed") {
				foundFindMissing = true
				break
			}
			if contains(log.Message, "all blobs indexed") {
				foundFindMissing = true
				break
			}
		}

		if !foundFindMissing {
			t.Log("Warning: Did not find find-missing polling in logs (may have been fast)")
		}
	})
}

func TestFindMissingAPI(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.Token == "" {
		t.Fatal("TOKEN not set in ~/.acemcp/settings.toml")
	}

	cfg.DataDir = t.TempDir()

	logger, err := logging.NewLogger("debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	svc := New(cfg, logger)

	t.Run("FindMissingWithUnknownBlobs", func(t *testing.T) {
		fakeBlobs := []string{
			"fake-blob-hash-12345",
			"fake-blob-hash-67890",
		}

		result, err := svc.findMissing(fakeBlobs)
		if err != nil {
			t.Fatalf("findMissing failed: %v", err)
		}

		t.Logf("Unknown blobs: %d", len(result.UnknownBlobNames))
		t.Logf("Nonindexed blobs: %d", len(result.NonindexedBlobNames))

		if len(result.UnknownBlobNames) != 2 {
			t.Errorf("Expected 2 unknown blobs, got %d", len(result.UnknownBlobNames))
		}
	})

	t.Run("FindMissingWithEmptyList", func(t *testing.T) {
		result, err := svc.findMissing([]string{})
		if err != nil {
			t.Fatalf("findMissing failed: %v", err)
		}

		t.Logf("Unknown blobs: %d", len(result.UnknownBlobNames))
		t.Logf("Nonindexed blobs: %d", len(result.NonindexedBlobNames))
	})
}

func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	fmt.Println("Running indexer integration tests...")
	os.Exit(m.Run())
}
