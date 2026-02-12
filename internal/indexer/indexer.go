package indexer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	gitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/meimingqi222/acemcp-go/internal/config"
	"github.com/meimingqi222/acemcp-go/internal/logging"
)

type blob struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type blobWithHash struct {
	blob
	Hash  string
	Mtime int64
	Size  int64
}

func (s *Service) removeFailed(projectPath string, hashes []string) {
	if len(hashes) == 0 {
		return
	}
	failed, err := s.loadFailed()
	if err != nil {
		s.logger.Warn("load failed blobs error", logging.Error(err))
		return
	}
	list := failed[projectPath]
	if len(list) == 0 {
		return
	}
	set := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		set[h] = struct{}{}
	}
	filtered := list[:0]
	for _, fb := range list {
		if _, ok := set[fb.BlobHash]; ok {
			continue
		}
		filtered = append(filtered, fb)
	}
	failed[projectPath] = filtered
	if err := s.saveFailed(failed); err != nil {
		s.logger.Warn("save failed blobs error", logging.Error(err))
	}
}

func (s *Service) ensureGitignoreLoaded(projectRoot string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.gitignores[projectRoot]; ok {
		return
	}

	entry := &gitignoreEntry{}
	gitignorePath := filepath.Join(projectRoot, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		s.gitignores[projectRoot] = entry
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var patterns []gitignore.Pattern
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	if len(patterns) > 0 {
		entry.matcher = gitignore.NewMatcher(patterns)
	}
	s.gitignores[projectRoot] = entry
}

func (s *Service) shouldSkip(projectRoot string, relPath string, isDir bool) bool {
	s.ensureGitignoreLoaded(projectRoot)

	s.mu.Lock()
	entry := s.gitignores[projectRoot]
	s.mu.Unlock()

	if entry != nil && entry.matcher != nil {
		parts := strings.Split(relPath, "/")
		if entry.matcher.Match(parts, isDir) {
			return true
		}
	}
	// Fallback: use configured exclude patterns
	for _, pat := range s.cfg.ExcludePatterns {
		if pat == "" {
			continue
		}
		if strings.Contains(relPath, pat) {
			return true
		}
		parts := strings.Split(relPath, "/")
		for _, p := range parts {
			if p == pat {
				return true
			}
		}
	}
	return false
}

// gitignoreEntry holds per-project gitignore matcher.
type gitignoreEntry struct {
	matcher gitignore.Matcher
}

// Service manages indexing and search operations.
type Service struct {
	logger       *logging.Logger
	cfg          *config.Config
	client       *http.Client
	projectsPath string
	failedPath   string
	filesPath    string
	opLog        *OpLogger

	opMu sync.Mutex

	mu             sync.Mutex
	watchers       map[string]*fsnotify.Watcher
	debounceTimers map[string]*time.Timer
	pending        map[string]*pendingChanges
	gitignores     map[string]*gitignoreEntry // per-project gitignore
	allowedExts    map[string]struct{}        // precompiled extension set

	cacheMu      sync.RWMutex
	projects     map[string][]string         // in-memory cache
	filesIdx     filesIndex                  // in-memory cache
	failed       map[string][]failedBlob     // in-memory cache
	cacheLoaded  bool
	cacheDirty   bool
	flushTimer   *time.Timer

	metricsMu      sync.Mutex
	indexRuns      int
	searchRuns     int
	lastIndexTime  time.Time
	lastSearchTime time.Time

	lastScanTimes map[string]time.Time // per-project last incremental scan time
}

// RefreshAllowedExts rebuilds allowed extensions from current config.
func (s *Service) RefreshAllowedExts() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.allowedExts = make(map[string]struct{}, len(s.cfg.TextExtensions))
	for _, e := range s.cfg.TextExtensions {
		s.allowedExts[strings.ToLower(e)] = struct{}{}
	}
}

func New(cfg *config.Config, logger *logging.Logger) *Service {
	_ = os.MkdirAll(cfg.DataDir, 0o755)
	allowedExts := make(map[string]struct{}, len(cfg.TextExtensions))
	for _, e := range cfg.TextExtensions {
		allowedExts[strings.ToLower(e)] = struct{}{}
	}
	return &Service{
		logger:         logger,
		cfg:            cfg,
		client:         &http.Client{Timeout: 90 * time.Second},
		projectsPath:   filepath.Join(cfg.DataDir, "projects.json"),
		failedPath:     filepath.Join(cfg.DataDir, "failed_blobs.json"),
		filesPath:      filepath.Join(cfg.DataDir, "files_index.json"),
		watchers:       make(map[string]*fsnotify.Watcher),
		debounceTimers: make(map[string]*time.Timer),
		pending:        make(map[string]*pendingChanges),
		gitignores:     make(map[string]*gitignoreEntry),
		allowedExts:    allowedExts,
		projects:       make(map[string][]string),
		filesIdx:       make(filesIndex),
		failed:         make(map[string][]failedBlob),
		opLog:          NewOpLogger(200),
		lastScanTimes:  make(map[string]time.Time),
	}
}

type IndexResult struct {
	Status string `json:"status"`
	// Message includes summary.
	Message string `json:"message"`
	// NewBlobs is the number of uploaded new blobs.
	NewBlobs int `json:"new_blobs"`
	// TotalBlobs is the total blobs tracked for the project after indexing.
	TotalBlobs int `json:"total_blobs"`
}

type SearchResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Output  string `json:"output,omitempty"`
}

type failedBlob struct {
	BlobHash  string `json:"blob_hash"`
	Path      string `json:"path"`
	Error     string `json:"error"`
	Timestamp string `json:"timestamp"`
}

type fileMetadata struct {
	Hashes []string `json:"hashes"`
	Mtime  int64    `json:"mtime"`
	Size   int64    `json:"size"`
}

type filesIndex map[string]map[string]fileMetadata // project_root -> file_path -> metadata

type pendingChanges struct {
	projectRoot string
	upsert      map[string]struct{}
	delete      map[string]struct{}
}

// ListProjects returns project -> blob count.
func (s *Service) ListProjects() (map[string]int, error) {
	projects, err := s.loadProjects()
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(projects))
	for k, v := range projects {
		out[k] = len(v)
	}
	return out, nil
}

func (s *Service) loadFilesIndex() (filesIndex, error) {
	return s.loadFilesIndexCached()
}

func (s *Service) saveFilesIndex(idx filesIndex) error {
	return s.saveFilesIndexCached(idx)
}

// ListFailed returns project -> failed blobs.
func (s *Service) ListFailed() (map[string][]failedBlob, error) {
	return s.loadFailed()
}

// WatcherDiagnostics contains diagnostic information about file watchers.
type WatcherDiagnostics struct {
	Project      string    `json:"project"`
	HasWatcher   bool      `json:"has_watcher"`
	LastScanTime string    `json:"last_scan_time"`
	ScanInterval string    `json:"scan_interval"`
}

// GetWatcherDiagnostics returns diagnostic information about all active watchers.
func (s *Service) GetWatcherDiagnostics() ([]WatcherDiagnostics, error) {
	projects, err := s.loadProjects()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var diags []WatcherDiagnostics
	for project := range projects {
		_, hasWatcher := s.watchers[project]
		lastScan := s.lastScanTimes[project]
		var lastScanStr string
		if lastScan.IsZero() {
			lastScanStr = "never"
		} else {
			lastScanStr = lastScan.Format(time.RFC3339)
		}
		diags = append(diags, WatcherDiagnostics{
			Project:      project,
			HasWatcher:   hasWatcher,
			LastScanTime: lastScanStr,
			ScanInterval: incrementalScanInterval.String(),
		})
	}

	return diags, nil
}

func (s *Service) normalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return strings.ReplaceAll(path, "\\", "/")
	}
	return strings.ReplaceAll(abs, "\\", "/")
}

// isSubPath checks if child is a subdirectory of parent.
func (s *Service) isSubPath(parent, child string) bool {
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != "."
}

// findChildProjects finds all indexed projects that are direct children of the given path.
func (s *Service) findChildProjects(parentPath string) []string {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()

	normParent := s.normalizePath(parentPath)
	var children []string
	for root := range s.projects {
		if root == normParent {
			continue
		}
		rel, err := filepath.Rel(normParent, root)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") && rel != "." {
			children = append(children, root)
		}
	}
	return children
}

// listCandidateChildDirs lists all potential child project directories under parentPath.
// This scans the filesystem to find directories that could be projects.
func (s *Service) listCandidateChildDirs(parentPath string) []string {
	entries, err := os.ReadDir(parentPath)
	if err != nil {
		s.logger.Debug("failed to read parent directory for child projects",
			logging.String("path", parentPath),
			logging.Error(err),
		)
		return nil
	}

	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip common non-project directories
		if name == "node_modules" || name == ".git" || name == "__pycache__" ||
			name == "vendor" || name == "build" || name == "dist" || name == ".vscode" ||
			name == ".idea" || name == "target" || name == "out" || strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(parentPath, name))
	}
	return dirs
}

func (s *Service) isAllowedExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := s.allowedExts[ext]
	return ok
}

type fileTask struct {
	absPath string
	relPath string
}

func (s *Service) collectBlobsWithHash(root string) ([]blobWithHash, error) {
	maxLines := s.cfg.MaxLinesPerBlob
	if maxLines <= 0 {
		maxLines = 800
	}

	numWorkers := runtime.NumCPU()
	if numWorkers < 2 {
		numWorkers = 2
	}
	if numWorkers > 8 {
		numWorkers = 8
	}

	tasks := make(chan fileTask, 256)
	results := make(chan []blobWithHash, 256)
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				blobs := s.processFile(root, task.absPath, task.relPath, maxLines)
				if len(blobs) > 0 {
					results <- blobs
				}
			}
		}()
	}

	go func() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			rel = strings.ReplaceAll(rel, "\\", "/")
			if d.IsDir() {
				if rel != "." && s.shouldSkip(root, rel, true) {
					return fs.SkipDir
				}
				return nil
			}
			if s.shouldSkip(root, rel, false) {
				return nil
			}
			if !s.isAllowedExt(path) {
				return nil
			}
			tasks <- fileTask{absPath: path, relPath: rel}
			return nil
		})
		close(tasks)
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	var allBlobs []blobWithHash
	for blobs := range results {
		allBlobs = append(allBlobs, blobs...)
	}

	return allBlobs, nil
}

func (s *Service) processFile(projectRoot, absPath, relPath string, maxLines int) []blobWithHash {
	f, err := os.Open(absPath)
	if err != nil {
		s.logger.Warn("read file failed", logging.String("file", relPath), logging.Error(err))
		return nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")

	if maxBytes := s.cfg.MaxLineBytes; maxBytes > 0 {
		for i, line := range lines {
			if len(line) > maxBytes {
				s.opLog.Warnf(OpCollect, s.normalizePath(projectRoot), "skipped %s: line %d too long (%d > %d)", relPath, i+1, len(line), maxBytes)
				return nil
			}
		}
	}

	if len(lines) <= maxLines {
		b := blob{Path: relPath, Content: content}
		return []blobWithHash{{blob: b, Hash: hashBlob(b), Mtime: info.ModTime().Unix(), Size: info.Size()}}
	}

	total := len(lines)
	chunks := (total + maxLines - 1) / maxLines
	result := make([]blobWithHash, 0, chunks)
	for i := 0; i < chunks; i++ {
		start := i * maxLines
		end := start + maxLines
		if end > total {
			end = total
		}
		chunkContent := strings.Join(lines[start:end], "\n")
		chunkPath := fmt.Sprintf("%s#chunk%dof%d", relPath, i+1, chunks)
		b := blob{Path: chunkPath, Content: chunkContent}
		result = append(result, blobWithHash{blob: b, Hash: hashBlob(b), Mtime: info.ModTime().Unix(), Size: info.Size()})
	}
	return result
}

func (s *Service) collectBlobs(root string) ([]blob, error) {
	blobs, err := s.collectBlobsWithHash(root)
	if err != nil {
		return nil, err
	}
	result := make([]blob, len(blobs))
	for i, b := range blobs {
		result[i] = b.blob
	}
	return result, nil
}

func hashBlob(b blob) string {
	h := sha256.New()
	h.Write([]byte(b.Path))
	h.Write([]byte(b.Content))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) ensureCacheLoaded() error {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cacheLoaded {
		return nil
	}
	b, err := os.ReadFile(s.projectsPath)
	if err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &s.projects)
	}
	b, err = os.ReadFile(s.filesPath)
	if err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &s.filesIdx); err != nil {
			// Try migration from v1 (map[string]map[string][]string)
			var v1 map[string]map[string][]string
			if errV1 := json.Unmarshal(b, &v1); errV1 == nil {
				s.logger.Info("migrating index from v1 to v2")
				s.filesIdx = make(filesIndex)
				for proj, files := range v1 {
					s.filesIdx[proj] = make(map[string]fileMetadata)
					for path, hashes := range files {
						s.filesIdx[proj][path] = fileMetadata{
							Hashes: hashes,
							Mtime:  0,
							Size:   0,
						}
					}
				}
				s.cacheDirty = true
			} else {
				s.logger.Warn("failed to load files index", logging.Error(err))
			}
		}
	}
	b, err = os.ReadFile(s.failedPath)
	if err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &s.failed)
	}
	s.cacheLoaded = true
	return nil
}

func (s *Service) scheduleCacheFlush() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cacheDirty = true
	if s.flushTimer != nil {
		return
	}
	s.flushTimer = time.AfterFunc(2*time.Second, func() {
		s.flushCache()
	})
}

func (s *Service) flushCache() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if !s.cacheDirty {
		return
	}
	s.flushTimer = nil
	_ = os.MkdirAll(filepath.Dir(s.projectsPath), 0o755)
	if data, err := json.Marshal(s.projects); err == nil {
		_ = os.WriteFile(s.projectsPath, data, 0o644)
	}
	if data, err := json.Marshal(s.filesIdx); err == nil {
		_ = os.WriteFile(s.filesPath, data, 0o644)
	}
	if data, err := json.Marshal(s.failed); err == nil {
		_ = os.WriteFile(s.failedPath, data, 0o644)
	}
	s.cacheDirty = false
}

func (s *Service) loadProjects() (map[string][]string, error) {
	_ = s.ensureCacheLoaded()
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	result := make(map[string][]string, len(s.projects))
	for k, v := range s.projects {
		cp := make([]string, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result, nil
}

func (s *Service) saveProjects(projects map[string][]string) error {
	s.cacheMu.Lock()
	s.projects = projects
	s.cacheMu.Unlock()
	s.scheduleCacheFlush()
	return nil
}

func (s *Service) loadFilesIndexCached() (filesIndex, error) {
	_ = s.ensureCacheLoaded()
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	result := make(filesIndex, len(s.filesIdx))
	for k, v := range s.filesIdx {
		cp := make(map[string]fileMetadata, len(v))
		for fk, fv := range v {
			hcp := make([]string, len(fv.Hashes))
			copy(hcp, fv.Hashes)
			cp[fk] = fileMetadata{
				Hashes: hcp,
				Mtime:  fv.Mtime,
				Size:   fv.Size,
			}
		}
		result[k] = cp
	}
	return result, nil
}

func (s *Service) saveFilesIndexCached(idx filesIndex) error {
	s.cacheMu.Lock()
	s.filesIdx = idx
	s.cacheMu.Unlock()
	s.scheduleCacheFlush()
	return nil
}

func (s *Service) loadFailed() (map[string][]failedBlob, error) {
	_ = s.ensureCacheLoaded()
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	result := make(map[string][]failedBlob, len(s.failed))
	for k, v := range s.failed {
		cp := make([]failedBlob, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result, nil
}

func (s *Service) saveFailed(data map[string][]failedBlob) error {
	s.cacheMu.Lock()
	s.failed = data
	s.cacheMu.Unlock()
	s.scheduleCacheFlush()
	return nil
}

func (s *Service) addFailed(projectPath, blobHash, blobPath, errMsg string) {
	now := time.Now().Format(time.RFC3339)
	failed, err := s.loadFailed()
	if err != nil {
		s.logger.Warn("load failed blobs", logging.Error(err))
		return
	}
	list := failed[projectPath]
	// replace if exists
	found := false
	for i := range list {
		if list[i].BlobHash == blobHash {
			list[i].Error = errMsg
			list[i].Timestamp = now
			found = true
		}
	}
	if !found {
		list = append(list, failedBlob{
			BlobHash:  blobHash,
			Path:      blobPath,
			Error:     errMsg,
			Timestamp: now,
		})
	}
	failed[projectPath] = list
	if err := s.saveFailed(failed); err != nil {
		s.logger.Warn("save failed blobs", logging.Error(err))
	}
}

func (s *Service) getFailedHashes(projectPath string) map[string]struct{} {
	failed, err := s.loadFailed()
	if err != nil {
		s.logger.Warn("load failed blobs", logging.Error(err))
		return nil
	}
	set := make(map[string]struct{})
	for _, fb := range failed[projectPath] {
		set[fb.BlobHash] = struct{}{}
	}
	return set
}

type findMissingResult struct {
	UnknownBlobNames    []string `json:"unknown_memory_names"`
	NonindexedBlobNames []string `json:"nonindexed_blob_names"`
}

type TokenRemoteCheck struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

func (s *Service) findMissing(blobNames []string) (*findMissingResult, error) {
	payload := map[string]any{
		"mem_object_names": blobNames,
	}
	body, _ := json.Marshal(payload)
	u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/find-missing")
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("find-missing failed: %s", string(b))
	}
	var res findMissingResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *Service) CheckToken(ctx context.Context) TokenRemoteCheck {
	start := time.Now()
	u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/find-missing")
	if err != nil {
		return TokenRemoteCheck{OK: false, Error: fmt.Sprintf("invalid base_url: %v", err)}
	}

	payload := map[string]any{
		"mem_object_names": []string{"__acemcp_token_check__"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TokenRemoteCheck{OK: false, Error: err.Error()}
	}

	endpoint := u.String()
	maxRetries := 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return TokenRemoteCheck{OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries-1 {
				time.Sleep(retryDelay(attempt))
				continue
			}
			return TokenRemoteCheck{OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}

		reqID := resp.Header.Get("x-request-id")
		if reqID == "" {
			reqID = resp.Header.Get("x-amzn-requestid")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			limited := io.LimitReader(resp.Body, 16*1024)
			b, _ := io.ReadAll(limited)
			_ = resp.Body.Close()

			retriable := resp.StatusCode == http.StatusRequestTimeout ||
				resp.StatusCode == http.StatusTooManyRequests ||
				(resp.StatusCode >= 500 && resp.StatusCode <= 599)
			if retriable && attempt < maxRetries-1 {
				time.Sleep(retryDelay(attempt))
				continue
			}

			return TokenRemoteCheck{
				OK:         false,
				StatusCode: resp.StatusCode,
				RequestID:  reqID,
				Error:      strings.TrimSpace(string(b)),
				DurationMs: time.Since(start).Milliseconds(),
			}
		}

		var out findMissingResult
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			lastErr = err
			_ = resp.Body.Close()
			if attempt < maxRetries-1 {
				time.Sleep(retryDelay(attempt))
				continue
			}
			return TokenRemoteCheck{OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		_ = resp.Body.Close()
		return TokenRemoteCheck{OK: true, StatusCode: resp.StatusCode, RequestID: reqID, DurationMs: time.Since(start).Milliseconds()}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("token check failed: unknown error")
	}
	return TokenRemoteCheck{OK: false, Error: lastErr.Error(), DurationMs: time.Since(start).Milliseconds()}
}

func (s *Service) waitForBlobsIndexed(normRoot string, blobNames []string) bool {
	if len(blobNames) == 0 {
		return true
	}

	maxWait := 60 * time.Second
	pollInterval := 3 * time.Second
	elapsed := time.Duration(0)
	lastNonindexed := -1
	stableCount := 0

	s.opLog.Infof(OpSearch, normRoot, "waiting for %d blobs to be indexed", len(blobNames))

	for elapsed < maxWait {
		time.Sleep(pollInterval)
		elapsed += pollInterval

		res, err := s.findMissing(blobNames)
		if err != nil {
			s.opLog.Warnf(OpSearch, normRoot, "find-missing failed: %v", err)
			continue
		}

		nonindexed := len(res.NonindexedBlobNames)
		unknown := len(res.UnknownBlobNames)

		if nonindexed == 0 && unknown == 0 {
			s.opLog.Infof(OpSearch, normRoot, "all blobs indexed after %v", elapsed)
			return true
		}

		s.opLog.Infof(OpSearch, normRoot, "waiting for index: %d nonindexed, %d unknown (%v elapsed)", nonindexed, unknown, elapsed)

		if nonindexed == lastNonindexed {
			stableCount++
			if stableCount >= 3 && nonindexed <= len(blobNames)/10 {
				s.opLog.Infof(OpSearch, normRoot, "index mostly complete: %d/%d blobs still processing, proceeding", nonindexed, len(blobNames))
				return true
			}
		} else {
			stableCount = 0
			lastNonindexed = nonindexed
		}
	}

	s.opLog.Warnf(OpSearch, normRoot, "timeout waiting for blobs to be indexed after %v", maxWait)
	return false
}

// waitForBlobsIndexedOptimized waits for blobs with sampling for large sets.
// For large projects, only checks a random sample of blobs instead of all.
// This significantly reduces API calls and CPU usage while still ensuring
// the majority of blobs are indexed.
func (s *Service) waitForBlobsIndexedOptimized(normRoot string, blobNames []string, isIncremental bool) bool {
	if len(blobNames) == 0 {
		return true
	}

	// For small sets, use full check
	if len(blobNames) <= 100 {
		return s.waitForBlobsIndexed(normRoot, blobNames)
	}

	// For larger sets, sample 100 blobs for incremental updates
	// (faster, acceptable for small % of changes)
	// For initial index, sample more aggressively but still check
	sampleSize := 100
	if isIncremental && len(blobNames) > sampleSize {
		sampleSize = min(100, len(blobNames)/10+10) // 10% + 10, max 100
	}

	// Simple deterministic sampling: pick every N-th blob
	step := len(blobNames) / sampleSize
	if step < 1 {
		step = 1
	}

	sampled := make([]string, 0, sampleSize)
	for i := 0; i < len(blobNames) && len(sampled) < sampleSize; i += step {
		sampled = append(sampled, blobNames[i])
	}

	// Use shorter timeout for incremental updates
	maxWait := 30 * time.Second
	if !isIncremental {
		maxWait = 60 * time.Second
	}
	pollInterval := 2 * time.Second
	elapsed := time.Duration(0)
	lastNonindexed := -1
	stableCount := 0

	s.opLog.Infof(OpSearch, normRoot, "waiting for %d blobs (sampled from %d, incremental=%v)",
		len(sampled), len(blobNames), isIncremental)

	for elapsed < maxWait {
		time.Sleep(pollInterval)
		elapsed += pollInterval

		res, err := s.findMissing(sampled)
		if err != nil {
			s.opLog.Warnf(OpSearch, normRoot, "find-missing failed: %v", err)
			continue
		}

		nonindexed := len(res.NonindexedBlobNames)
		unknown := len(res.UnknownBlobNames)

		// Sample is ready if all sampled blobs are indexed
		if nonindexed == 0 && unknown == 0 {
			s.opLog.Infof(OpSearch, normRoot, "sampled blobs indexed after %v", elapsed)
			return true
		}

		// For incremental updates, allow up to 5% failure in sample
		failureRate := float64(nonindexed+unknown) / float64(len(sampled))
		if isIncremental && failureRate <= 0.05 && stableCount >= 2 {
			s.opLog.Infof(OpSearch, normRoot, "incremental index mostly ready: %.1f%% not indexed, proceeding",
				failureRate*100)
			return true
		}

		s.opLog.Debug(OpSearch, normRoot, fmt.Sprintf("waiting for index: %d nonindexed, %d unknown (sample %d/%d)",
			nonindexed, unknown, len(sampled), len(blobNames)))

		if nonindexed == lastNonindexed {
			stableCount++
		} else {
			stableCount = 0
			lastNonindexed = nonindexed
		}
	}

	// Timeout - for incremental, proceed anyway with warning
	if isIncremental {
		s.opLog.Warnf(OpSearch, normRoot, "timeout waiting for incremental index after %v, proceeding anyway", maxWait)
		return true
	}

	s.opLog.Warnf(OpSearch, normRoot, "timeout waiting for blobs to be indexed after %v", maxWait)
	return false
}

type apiHTTPError struct {
	StatusCode int
	Body       string
	RequestID  string
}

func (e *apiHTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 1024 {
		body = body[:1024]
	}
	if e.RequestID != "" {
		return fmt.Sprintf("upload failed (status %d, request_id %s): %s", e.StatusCode, e.RequestID, body)
	}
	return fmt.Sprintf("upload failed (status %d): %s", e.StatusCode, body)
}

func statusCodeFromErr(err error) (int, bool) {
	var he *apiHTTPError
	if errors.As(err, &he) {
		return he.StatusCode, true
	}
	return 0, false
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := 300 * time.Millisecond
	max := 5 * time.Second
	d := base * time.Duration(1<<attempt)
	if d > max {
		d = max
	}
	jitter := time.Duration(time.Now().UnixNano()%int64(200*time.Millisecond))
	return d + jitter
}

func (s *Service) uploadBlobs(blobs []blob) ([]string, error) {
	payload := map[string]any{
		"blobs": blobs,
	}
	u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/batch-upload")
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := u.String()
	maxRetries := 5
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			if attempt < maxRetries-1 {
				s.logger.Warn("upload request failed, retrying",
					logging.Error(err),
					logging.Int("attempt", attempt+1),
				)
				time.Sleep(retryDelay(attempt))
				continue
			}
			return nil, err
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			limited := io.LimitReader(resp.Body, 16*1024)
			b, _ := io.ReadAll(limited)
			_ = resp.Body.Close()
			reqID := resp.Header.Get("x-request-id")
			if reqID == "" {
				reqID = resp.Header.Get("x-amzn-requestid")
			}
			he := &apiHTTPError{StatusCode: resp.StatusCode, Body: string(b), RequestID: reqID}
			lastErr = he
			retriable := resp.StatusCode == http.StatusRequestTimeout ||
				resp.StatusCode == http.StatusTooManyRequests ||
				(resp.StatusCode >= 500 && resp.StatusCode <= 599)
			if retriable && attempt < maxRetries-1 {
				s.logger.Warn("upload failed, retrying",
					logging.Int("status_code", resp.StatusCode),
					logging.String("request_id", reqID),
					logging.Int("attempt", attempt+1),
				)
				time.Sleep(retryDelay(attempt))
				continue
			}
			return nil, he
		}

		var res struct {
			BlobNames []string `json:"blob_names"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			lastErr = err
			_ = resp.Body.Close()
			if attempt < maxRetries-1 {
				s.logger.Warn("upload response decode failed, retrying",
					logging.Error(err),
					logging.Int("attempt", attempt+1),
				)
				time.Sleep(retryDelay(attempt))
				continue
			}
			return nil, err
		}
		_ = resp.Body.Close()
		return res.BlobNames, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("upload failed: unknown error")
	}
	return nil, lastErr
}

// IndexProject collects files, uploads new blobs, and updates project state.
// Note: Each path is indexed independently. Parent and child projects maintain separate indexes.
// For parent directory search, use SearchContext which aggregates child project results.
func (s *Service) IndexProject(projectRoot string) (*IndexResult, error) {
	startTime := time.Now()
	if projectRoot == "" {
		return nil, fmt.Errorf("project_root_path required")
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
	normRoot := s.normalizePath(projectRoot)
	s.StartWatching(projectRoot)

	s.metricsMu.Lock()
	s.indexRuns++
	s.lastIndexTime = time.Now()
	s.metricsMu.Unlock()

	s.opLog.Infof(OpCollect, normRoot, "starting file collection")

	blobs, err := s.collectBlobsWithHash(projectRoot)
	if err != nil {
		s.opLog.Errorf(OpCollect, normRoot, "collect failed: %v", err)
		return nil, err
	}
	if len(blobs) == 0 {
		s.opLog.Warn(OpCollect, normRoot, "no text files found", "")
		return &IndexResult{Status: "error", Message: "no text files found"}, nil
	}

	s.opLog.Infof(OpCollect, normRoot, "collected %d blobs from %d files", len(blobs), len(blobs))
	projects, err := s.loadProjects()
	if err != nil {
		return nil, err
	}
	filesIdx, err := s.loadFilesIndexCached()
	if err != nil {
		return nil, err
	}
	prevFiles := filesIdx[normRoot]
	if prevFiles == nil {
		prevFiles = map[string]fileMetadata{}
	}
	currentFiles := map[string]fileMetadata{}
	currentHashes := map[string]struct{}{}
	
	// Group blobs by file to update metadata
	blobsByFile := make(map[string][]blobWithHash)
	for _, b := range blobs {
		base := b.Path
		if i := strings.Index(base, "#chunk"); i >= 0 {
			base = base[:i]
		}
		blobsByFile[base] = append(blobsByFile[base], b)
		currentHashes[b.Hash] = struct{}{}
	}

	for relPath, fblobs := range blobsByFile {
		hashes := make([]string, len(fblobs))
		for i, b := range fblobs {
			hashes[i] = b.Hash
		}
		
		// Use metadata from the first chunk (all chunks have same mtime/size from processFile)
		var mtime, size int64
		if len(fblobs) > 0 {
			mtime = fblobs[0].Mtime
			size = fblobs[0].Size
		}

		currentFiles[relPath] = fileMetadata{
			Hashes: hashes,
			Mtime:  mtime,
			Size:   size,
		}
	}

	removed := make([]string, 0)
	for _, meta := range prevFiles {
		for _, h := range meta.Hashes {
			if _, ok := currentHashes[h]; !ok {
				removed = append(removed, h)
			}
		}
	}

	existing := map[string]struct{}{}
	for _, h := range projects[normRoot] {
		if _, ok := currentHashes[h]; ok {
			existing[h] = struct{}{}
		}
	}

	if len(removed) > 0 {
		s.removeFailed(normRoot, removed)
	}

	var blobsToUpload []blobWithHash
	for _, b := range blobs {
		if _, ok := existing[b.Hash]; ok {
			continue
		}
		blobsToUpload = append(blobsToUpload, b)
	}

	s.opLog.Infof(OpUpload, normRoot, "uploading %d new blobs (existing: %d)", len(blobsToUpload), len(existing))

	uploaded := s.uploadBlobsConcurrent(normRoot, blobsToUpload)

	if len(blobsToUpload) > 0 && len(uploaded) < len(blobsToUpload) {
		s.opLog.Warnf(OpUpload, normRoot, "partial upload: %d/%d succeeded", len(uploaded), len(blobsToUpload))
	} else if len(uploaded) > 0 {
		s.opLog.Infof(OpUpload, normRoot, "uploaded %d blobs successfully", len(uploaded))
	}

	newProject := make([]string, 0, len(existing)+len(uploaded))
	for h := range existing {
		newProject = append(newProject, h)
	}
	newProject = append(newProject, uploaded...)
	projects[normRoot] = newProject

	filesIdx[normRoot] = currentFiles
	if err := s.saveFilesIndexCached(filesIdx); err != nil {
		return nil, err
	}
	if err := s.saveProjects(projects); err != nil {
		return nil, err
	}

	duration := time.Since(startTime)
	s.opLog.Log(OpIndex, normRoot,
		fmt.Sprintf("indexed %d blobs (new %d)", len(projects[normRoot]), len(uploaded)),
		duration, true, "")

	s.logger.Info("index completed",
		logging.String("project", normRoot),
		logging.Int("total_blobs", len(projects[normRoot])),
		logging.Int("new_blobs", len(uploaded)),
		logging.Int("collected", len(blobs)),
		logging.Int64("duration_ms", duration.Milliseconds()),
	)

	return &IndexResult{
		Status:     "success",
		Message:    fmt.Sprintf("indexed %d total blobs (new %d)", len(projects[normRoot]), len(uploaded)),
		NewBlobs:   len(uploaded),
		TotalBlobs: len(projects[normRoot]),
	}, nil
}

type uploadResult struct {
	hashes []string
	failed []blobWithHash
}

func (s *Service) uploadBlobsConcurrent(normRoot string, blobs []blobWithHash) []string {
	if len(blobs) == 0 {
		return nil
	}
	batchSize := s.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	var batches [][]blobWithHash
	for i := 0; i < len(blobs); i += batchSize {
		end := i + batchSize
		if end > len(blobs) {
			end = len(blobs)
		}
		batches = append(batches, blobs[i:end])
	}

	concurrency := 4
	if len(batches) < concurrency {
		concurrency = len(batches)
	}
	sem := make(chan struct{}, concurrency)
	resultCh := make(chan uploadResult, len(batches))

	var wg sync.WaitGroup
	for _, batch := range batches {
		wg.Add(1)
		sem <- struct{}{}
		go func(b []blobWithHash) {
			defer wg.Done()
			defer func() { <-sem }()
			res := s.uploadBatch(normRoot, b)
			resultCh <- res
		}(batch)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var uploaded []string
	for res := range resultCh {
		uploaded = append(uploaded, res.hashes...)
	}
	return uploaded
}

func (s *Service) uploadBatchSplit(normRoot string, batch []blobWithHash, depth int) uploadResult {
	if len(batch) == 0 {
		return uploadResult{}
	}

	if len(batch) == 1 {
		b := batch[0]
		_, err := s.uploadBlobs([]blob{b.blob})
		if err != nil {
			s.opLog.Error(OpUpload, normRoot, fmt.Sprintf("upload failed: %s", b.Path), err.Error())
			s.logger.Warn("individual blob upload failed",
				logging.String("path", b.Path),
				logging.Error(err),
			)
			s.addFailed(normRoot, b.Hash, b.Path, err.Error())
			return uploadResult{failed: []blobWithHash{b}}
		}
		return uploadResult{hashes: []string{b.Hash}}
	}

	blobs := make([]blob, len(batch))
	hashes := make([]string, len(batch))
	for i, b := range batch {
		blobs[i] = b.blob
		hashes[i] = b.Hash
	}

	_, err := s.uploadBlobs(blobs)
	if err == nil {
		return uploadResult{hashes: hashes}
	}

	if depth <= 0 {
		var uploaded []string
		var failed []blobWithHash
		for _, b := range batch {
			_, berr := s.uploadBlobs([]blob{b.blob})
			if berr != nil {
				s.opLog.Error(OpUpload, normRoot, fmt.Sprintf("upload failed: %s", b.Path), berr.Error())
				s.logger.Warn("individual blob upload failed",
					logging.String("path", b.Path),
					logging.Error(berr),
				)
				s.addFailed(normRoot, b.Hash, b.Path, berr.Error())
				failed = append(failed, b)
				continue
			}
			uploaded = append(uploaded, b.Hash)
		}
		return uploadResult{hashes: uploaded, failed: failed}
	}

	mid := len(batch) / 2
	left := s.uploadBatchSplit(normRoot, batch[:mid], depth-1)
	right := s.uploadBatchSplit(normRoot, batch[mid:], depth-1)
	return uploadResult{
		hashes: append(left.hashes, right.hashes...),
		failed: append(left.failed, right.failed...),
	}
}

func (s *Service) uploadBatch(normRoot string, batch []blobWithHash) uploadResult {
	blobs := make([]blob, len(batch))
	hashes := make([]string, len(batch))
	for i, b := range batch {
		blobs[i] = b.blob
		hashes[i] = b.Hash
	}

	_, err := s.uploadBlobs(blobs)
	if err == nil {
		s.removeFailed(normRoot, hashes)
		return uploadResult{hashes: hashes}
	}

	s.opLog.Warnf(OpUpload, normRoot, "batch upload failed (%d blobs), falling back to smaller batches: %v", len(batch), err)
	s.logger.Warn("batch upload failed, falling back to smaller batches",
		logging.Error(err),
		logging.Int("batch_size", len(batch)),
	)

	res := s.uploadBatchSplit(normRoot, batch, 2)
	if len(res.hashes) > 0 {
		s.removeFailed(normRoot, res.hashes)
	}
	if len(res.failed) > 0 {
		s.opLog.Warnf(OpUpload, normRoot, "fallback complete: %d succeeded, %d failed", len(res.hashes), len(res.failed))
	} else {
		s.opLog.Infof(OpUpload, normRoot, "fallback complete: %d succeeded, 0 failed", len(res.hashes))
	}
	return uploadResult{hashes: res.hashes, failed: res.failed}
}

func (s *Service) isAllowedTextExt(path string) bool {
	return s.isAllowedExt(path)
}

func (s *Service) collectFileBlobs(projectRoot string, relPath string) ([]blob, error) {
	if relPath == "" || relPath == "." {
		return nil, nil
	}
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	if s.shouldSkip(projectRoot, relPath, false) {
		return nil, nil
	}
	if !s.isAllowedTextExt(relPath) {
		return nil, nil
	}
	abs := filepath.Join(projectRoot, filepath.FromSlash(relPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	content := string(data)
	maxLines := s.cfg.MaxLinesPerBlob
	if maxLines <= 0 {
		maxLines = 800
	}
	lines := strings.Split(content, "\n")
	if maxBytes := s.cfg.MaxLineBytes; maxBytes > 0 {
		for i, line := range lines {
			if len(line) > maxBytes {
				s.opLog.Warnf(OpCollect, s.normalizePath(projectRoot), "skipped %s: line %d too long (%d > %d)", relPath, i+1, len(line), maxBytes)
				return nil, nil
			}
		}
	}
	if len(lines) <= maxLines {
		return []blob{{Path: relPath, Content: content}}, nil
	}

	total := len(lines)
	chunks := (total + maxLines - 1) / maxLines
	out := make([]blob, 0, chunks)
	for i := 0; i < chunks; i++ {
		start := i * maxLines
		end := start + maxLines
		if end > total {
			end = total
		}
		chunkContent := strings.Join(lines[start:end], "\n")
		chunkPath := fmt.Sprintf("%s#chunk%dof%d", relPath, i+1, chunks)
		out = append(out, blob{Path: chunkPath, Content: chunkContent})
	}
	return out, nil
}

func removeHashesFromSlice(in []string, remove map[string]struct{}) []string {
	if len(in) == 0 || len(remove) == 0 {
		return in
	}
	out := in[:0]
	for _, h := range in {
		if _, ok := remove[h]; ok {
			continue
		}
		out = append(out, h)
	}
	return out
}

func (s *Service) ApplyFileChanges(projectRoot string, upserts []string, deletes []string) error {
	if projectRoot == "" {
		return fmt.Errorf("project_root_path required")
	}
	startTime := time.Now()
	s.opMu.Lock()
	defer s.opMu.Unlock()

	normRoot := s.normalizePath(projectRoot)
	s.logger.Debug("ApplyFileChanges called",
		logging.String("project", normRoot),
		logging.Int("upserts", len(upserts)),
		logging.Int("deletes", len(deletes)),
	)
	s.opLog.Infof(OpApply, normRoot, "applying changes: %d upserts, %d deletes", len(upserts), len(deletes))
	projects, err := s.loadProjects()
	if err != nil {
		return err
	}
	filesIdx, err := s.loadFilesIndex()
	if err != nil {
		return err
	}
	prevFiles := filesIdx[normRoot]
	if prevFiles == nil {
		prevFiles = map[string]fileMetadata{}
	}
	projectSet := make(map[string]struct{}, len(projects[normRoot]))
	for _, h := range projects[normRoot] {
		projectSet[h] = struct{}{}
	}

	removedHashes := make([]string, 0)
	for _, rel := range deletes {
		rel = strings.ReplaceAll(rel, "\\", "/")
		meta := prevFiles[rel]
		if len(meta.Hashes) == 0 {
			delete(prevFiles, rel)
			continue
		}
		for _, h := range meta.Hashes {
			removedHashes = append(removedHashes, h)
			delete(projectSet, h)
		}
		delete(prevFiles, rel)
	}

	for _, rel := range upserts {
		rel = strings.ReplaceAll(rel, "\\", "/")
		if rel == "" || rel == "." {
			continue
		}
		abs := filepath.Join(projectRoot, filepath.FromSlash(rel))
		info, statErr := os.Stat(abs)
		if statErr != nil {
			meta := prevFiles[rel]
			if len(meta.Hashes) > 0 {
				for _, h := range meta.Hashes {
					removedHashes = append(removedHashes, h)
					delete(projectSet, h)
				}
			}
			delete(prevFiles, rel)
			continue
		}
		if info.IsDir() {
			continue
		}
		if s.shouldSkip(projectRoot, rel, false) || !s.isAllowedTextExt(rel) {
			meta := prevFiles[rel]
			if len(meta.Hashes) > 0 {
				for _, h := range meta.Hashes {
					removedHashes = append(removedHashes, h)
					delete(projectSet, h)
				}
			}
			delete(prevFiles, rel)
			continue
		}

		blobs, berr := s.collectFileBlobs(projectRoot, rel)
		if berr != nil {
			s.logger.Warn("collect file blobs failed", logging.String("file", rel), logging.Error(berr))
			continue
		}
		if len(blobs) == 0 {
			continue
		}

		newHashes := make([]string, 0, len(blobs))
		newSet := make(map[string]struct{}, len(blobs))
		for _, b := range blobs {
			h := hashBlob(b)
			newHashes = append(newHashes, h)
			newSet[h] = struct{}{}
		}

		meta := prevFiles[rel]
		if len(meta.Hashes) > 0 {
			for _, h := range meta.Hashes {
				if _, ok := newSet[h]; ok {
					continue
				}
				removedHashes = append(removedHashes, h)
				delete(projectSet, h)
			}
		}

		var toUpload []blob
		var toUploadHashes []string
		for i, b := range blobs {
			h := newHashes[i]
			if _, ok := projectSet[h]; ok {
				continue
			}
			toUpload = append(toUpload, b)
			toUploadHashes = append(toUploadHashes, h)
		}

		if len(toUpload) > 0 {
			batchSize := s.cfg.BatchSize
			if batchSize <= 0 {
				batchSize = 10
			}
			for i := 0; i < len(toUpload); i += batchSize {
				end := i + batchSize
				if end > len(toUpload) {
					end = len(toUpload)
				}
				_, uerr := s.uploadBlobs(toUpload[i:end])
				if uerr == nil {
					s.removeFailed(normRoot, toUploadHashes[i:end])
					for _, h := range toUploadHashes[i:end] {
						projectSet[h] = struct{}{}
					}
					continue
				}
				for j := i; j < end; j++ {
					_, berr := s.uploadBlobs([]blob{toUpload[j]})
					if berr != nil {
						s.addFailed(normRoot, toUploadHashes[j], toUpload[j].Path, berr.Error())
						continue
					}
					projectSet[toUploadHashes[j]] = struct{}{}
					s.removeFailed(normRoot, []string{toUploadHashes[j]})
				}
			}
		}

		prevFiles[rel] = fileMetadata{
			Hashes: newHashes,
			Mtime:  info.ModTime().Unix(),
			Size:   info.Size(),
		}
	}

	if len(removedHashes) > 0 {
		s.removeFailed(normRoot, removedHashes)
	}

	newProject := make([]string, 0, len(projectSet))
	for h := range projectSet {
		newProject = append(newProject, h)
	}
	projects[normRoot] = newProject
	filesIdx[normRoot] = prevFiles
	if err := s.saveFilesIndex(filesIdx); err != nil {
		return err
	}
	if err := s.saveProjects(projects); err != nil {
		return err
	}
	duration := time.Since(startTime)
	s.opLog.Log(OpApply, normRoot, fmt.Sprintf("changes applied: %d upserts, %d deletes", len(upserts), len(deletes)), duration, true, "")
	return nil
}

// initialIndexDelay is removed - we now use find-missing API to poll for index readiness

// incrementalScanInterval defines how often to run incremental scans to detect missed files.
const incrementalScanInterval = 3 * time.Minute

// incrementalScan checks for files that exist on disk but are missing from the index.
// This catches files that were created while the watcher was not running or missed events.
func (s *Service) incrementalScan(projectRoot, normRoot string) {
	s.mu.Lock()
	lastScan := s.lastScanTimes[normRoot]
	if time.Since(lastScan) < incrementalScanInterval {
		s.mu.Unlock()
		s.logger.Debug("incremental scan skipped (too recent)",
			logging.String("project", normRoot),
			logging.String("last_scan", lastScan.Format(time.RFC3339)),
			logging.String("interval", incrementalScanInterval.String()),
		)
		return
	}
	s.lastScanTimes[normRoot] = time.Now()
	s.mu.Unlock()

	s.logger.Info("starting incremental scan",
		logging.String("project", normRoot),
		logging.String("last_scan", lastScan.Format(time.RFC3339)),
	)
	s.opLog.Info(OpIndex, normRoot, "starting incremental scan for missed files")

	filesIdx, err := s.loadFilesIndex()
	if err != nil {
		s.opLog.Errorf(OpIndex, normRoot, "incremental scan: load files index failed: %v", err)
		return
	}

	indexed := filesIdx[normRoot]
	if indexed == nil {
		indexed = make(map[string]fileMetadata)
	}

	var missingFiles []string
	var scannedFiles int
	_ = filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, relErr := filepath.Rel(projectRoot, path)
			if relErr == nil {
				rel = strings.ReplaceAll(rel, "\\", "/")
				if rel != "." && s.shouldSkip(projectRoot, rel, true) {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !s.isAllowedExt(path) {
			return nil
		}
		rel, relErr := filepath.Rel(projectRoot, path)
		if relErr != nil {
			return nil
		}
		rel = strings.ReplaceAll(rel, "\\", "/")
		if s.shouldSkip(projectRoot, rel, false) {
			return nil
		}
		scannedFiles++
		
		meta, exists := indexed[rel]
		if !exists {
			missingFiles = append(missingFiles, rel)
			return nil
		}
		
		info, err := d.Info()
		if err != nil {
			return nil
		}
		
		if meta.Mtime != info.ModTime().Unix() || meta.Size != info.Size() {
			missingFiles = append(missingFiles, rel)
		}
		return nil
	})

	if len(missingFiles) == 0 {
		s.logger.Info("incremental scan complete",
			logging.String("project", normRoot),
			logging.Int("scanned_files", scannedFiles),
			logging.Int("indexed_files", len(indexed)),
			logging.Int("changed_files", 0),
		)
		s.opLog.Info(OpIndex, normRoot, "incremental scan complete: no missing or changed files")
		return
	}

	s.logger.Info("incremental scan found missing or changed files",
		logging.String("project", normRoot),
		logging.Int("scanned_files", scannedFiles),
		logging.Int("indexed_files", len(indexed)),
		logging.Int("changed_files", len(missingFiles)),
	)
	s.opLog.Infof(OpIndex, normRoot, "incremental scan found %d missing or changed files, scheduling for indexing", len(missingFiles))

	// Log each changed file for debugging
	for i, relPath := range missingFiles {
		if i < 10 { // Log first 10 changed files
			s.logger.Debug("changed file detected",
				logging.String("project", normRoot),
				logging.String("file", relPath),
			)
		}
		s.scheduleFileChange(normRoot, projectRoot, relPath, false, 100*time.Millisecond)
	}
	if len(missingFiles) > 10 {
		s.logger.Debug("... and more changed files",
			logging.String("project", normRoot),
			logging.Int("additional_count", len(missingFiles)-10),
		)
	}
}

// SearchContext ensures indexing, then performs search via remote API.
func (s *Service) SearchContext(projectRoot, query string) (*SearchResult, error) {
	startTime := time.Now()
	var indexMs int64
	if projectRoot == "" || query == "" {
		return nil, fmt.Errorf("project_root_path and query required")
	}
	s.metricsMu.Lock()
	s.searchRuns++
	s.lastSearchTime = time.Now()
	s.metricsMu.Unlock()

	normRoot := s.normalizePath(projectRoot)
	s.opLog.Infof(OpSearch, normRoot, "search started: %s", query)
	s.StartWatching(projectRoot)

	s.opMu.Lock()
	projects, err := s.loadProjects()
	s.opMu.Unlock()
	if err != nil {
		s.opLog.Errorf(OpSearch, normRoot, "load projects failed: %v", err)
		return nil, err
	}
	indexStart := time.Now()
	justIndexed := false
	hasPendingChanges := false // track if there are pending file changes
	var childProjects []string
	var useChildAggregation bool

	// Check for indexed child projects (always check, regardless of whether normRoot is indexed)
	indexedChildren := s.findChildProjects(projectRoot)
	if len(indexedChildren) > 0 {
		s.opLog.Infof(OpSearch, normRoot,
			"found %d indexed child projects, checking for unindexed children", len(indexedChildren))

		// Find candidate child dirs on filesystem
		candidateDirs := s.listCandidateChildDirs(projectRoot)

		// Identify which candidates are not yet indexed
		indexedSet := make(map[string]struct{}, len(indexedChildren))
		for _, c := range indexedChildren {
			indexedSet[c] = struct{}{}
		}

		var unindexed []string
		for _, dir := range candidateDirs {
			normDir := s.normalizePath(dir)
			if _, ok := indexedSet[normDir]; !ok {
				unindexed = append(unindexed, normDir)
			}
		}

		if len(unindexed) > 0 {
			s.opLog.Infof(OpSearch, normRoot,
				"found %d unindexed child projects, indexing them first", len(unindexed))
			// Index unindexed child projects
			for _, childRoot := range unindexed {
				s.opLog.Infof(OpSearch, normRoot, "indexing child project: %s", childRoot)
				if _, err := s.IndexProject(childRoot); err != nil {
					s.opLog.Warnf(OpSearch, normRoot, "failed to index child project %s: %v", childRoot, err)
				}
			}
			// Reload projects to get updated list
			s.opMu.Lock()
			projects, err = s.loadProjects()
			s.opMu.Unlock()
			if err != nil {
				s.opLog.Errorf(OpSearch, normRoot, "reload projects failed: %v", err)
				return nil, err
			}
			// Re-fetch indexed children
			indexedChildren = s.findChildProjects(projectRoot)
		}

		childProjects = indexedChildren
		useChildAggregation = true
		pendingChanges := s.flushPendingChanges(normRoot)
		hasPendingChanges = pendingChanges > 0
		if hasPendingChanges {
			s.opLog.Infof(OpSearch, normRoot, "detected %d pending file changes, waiting for indexing", pendingChanges)
		}
	} else if len(projects[normRoot]) == 0 {
		// No child projects and this path not indexed - index this path
		s.opLog.Info(OpSearch, normRoot, "project not indexed, starting initial index")
		if _, err := s.IndexProject(projectRoot); err != nil {
			s.opLog.Errorf(OpSearch, normRoot, "initial index failed: %v", err)
			return nil, err
		}
		justIndexed = true
	} else {
		// This path is indexed and has no child projects
		// Flush any pending changes and track if there are new files
		pendingChanges := s.flushPendingChanges(normRoot)
		hasPendingChanges = pendingChanges > 0

		// Trigger async incremental scan to detect new/modified files
		go s.incrementalScan(projectRoot, normRoot)

		// Note: don't wait here, will wait after getting blob names
	}
	indexMs = time.Since(indexStart).Milliseconds()

	s.opMu.Lock()
	projects, err = s.loadProjects()
	s.opMu.Unlock()
	if err != nil {
		return nil, err
	}

	// Get blob names: use child aggregation if available, otherwise use own index
	var blobNames []string
	if useChildAggregation {
		// Collect blobs from all child projects (with deduplication)
		seen := make(map[string]struct{})
		for _, childRoot := range childProjects {
			for _, h := range projects[childRoot] {
				if _, ok := seen[h]; !ok {
					seen[h] = struct{}{}
					blobNames = append(blobNames, h)
				}
			}
		}
		if len(blobNames) == 0 {
			s.opLog.Error(OpSearch, normRoot, "no blobs found in child projects", "")
			return nil, fmt.Errorf("no blobs found in child projects")
		}
		s.opLog.Infof(OpSearch, normRoot, "searching across %d blobs from %d child projects",
			len(blobNames), len(childProjects))
	} else {
		// Use own index
		blobNames = projects[normRoot]
		if len(blobNames) == 0 {
			s.opLog.Error(OpSearch, normRoot, "no blobs found after indexing", "")
			return nil, fmt.Errorf("no blobs found for project")
		}
	}

	// Collect failed blobs: use all child projects' failed blobs for aggregation
	var failedSet map[string]struct{}
	if useChildAggregation {
		failedSet = make(map[string]struct{})
		for _, childRoot := range childProjects {
			childFailed := s.getFailedHashes(childRoot)
			for h := range childFailed {
				failedSet[h] = struct{}{}
			}
		}
	} else {
		failedSet = s.getFailedHashes(normRoot)
	}
	if len(failedSet) > 0 {
		filtered := blobNames[:0]
		for _, h := range blobNames {
			if _, bad := failedSet[h]; bad {
				continue
			}
			filtered = append(filtered, h)
		}
		blobNames = filtered
		s.opLog.Infof(OpSearch, normRoot, "filtered out %d failed blobs, %d remaining", len(failedSet), len(blobNames))
	}
	if len(blobNames) == 0 {
		s.opLog.Error(OpSearch, normRoot, "no valid blobs available (all failed)", "")
		return nil, fmt.Errorf("no valid blobs available for search")
	}

	s.opLog.Infof(OpSearch, normRoot, "calling search API with %d blobs", len(blobNames))

	// Wait for remote index to be ready using optimized polling
	// - Initial index (justIndexed): wait for all blobs, up to 60s
	// - Incremental updates (hasPendingChanges): sample-based check, up to 30s
	// - Normal search: no wait needed
	if justIndexed {
		// First-time index: wait for all blobs
		s.waitForBlobsIndexed(normRoot, blobNames)
	} else if hasPendingChanges {
		// Has pending changes: use optimized sampling for faster response
		s.waitForBlobsIndexedOptimized(normRoot, blobNames, true)
	}

	payload := map[string]any{
		"information_request": query,
		"blobs": map[string]any{
			"checkpoint_id": nil,
			"added_blobs":   blobNames,
			"deleted_blobs": []string{},
		},
		"dialog":                     []any{},
		"max_output_length":          0,
		"disable_codebase_retrieval": false,
		"enable_commit_retrieval":    false,
	}

	// Search with retry mechanism
	var res struct {
		Formatted string `json:"formatted_retrieval"`
	}
	maxRetries := 3
	baseDelay := 2 * time.Second
	var apiMs int64
	for attempt := 0; attempt < maxRetries; attempt++ {
		body, _ := json.Marshal(payload)
		u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/agents/codebase-retrieval")
		if err != nil {
			return nil, fmt.Errorf("invalid base_url: %w", err)
		}
		req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
		req.Header.Set("Content-Type", "application/json")

		apiStart := time.Now()
		resp, err := s.client.Do(req)
		if err != nil {
			s.logger.Warn("search request failed, retrying",
				logging.Error(err),
				logging.Int("attempt", attempt+1),
			)
			if attempt < maxRetries-1 {
				delay := baseDelay * time.Duration(attempt+1)
				s.opLog.Warnf(OpSearch, normRoot, "search request failed, retrying in %v (attempt %d/%d)", delay, attempt+1, maxRetries)
				time.Sleep(delay)
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			b, _ := io.ReadAll(resp.Body)
			s.opLog.Error(OpSearch, normRoot, fmt.Sprintf("search API error (HTTP %d)", resp.StatusCode), string(b))
			if attempt < maxRetries-1 {
				delay := baseDelay * time.Duration(attempt+1)
				time.Sleep(delay)
				continue
			}
			return nil, fmt.Errorf("search failed: %s", string(b))
		}

		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			s.opLog.Errorf(OpSearch, normRoot, "decode response failed: %v", err)
			if attempt < maxRetries-1 {
				delay := baseDelay * time.Duration(attempt+1)
				time.Sleep(delay)
				continue
			}
			return nil, err
		}
		apiMs = time.Since(apiStart).Milliseconds()

		// Check if result is empty (index may still be building)
		if res.Formatted == "" || len(res.Formatted) < 10 {
			s.opLog.Warnf(OpSearch, normRoot, "search returned empty/short result (len=%d), retrying (attempt %d/%d)", len(res.Formatted), attempt+1, maxRetries)
			if attempt < maxRetries-1 {
				delay := baseDelay * time.Duration(attempt+1)
				time.Sleep(delay)
				continue
			}
			res.Formatted = "No relevant code context found."
			break
		}
		break
	}

	resultLen := len(res.Formatted)
	if res.Formatted == "No relevant code context found." {
		s.opLog.Warn(OpSearch, normRoot, "search returned empty result", "API returned no matching code")
	} else {
		s.opLog.Infof(OpSearch, normRoot, "search completed, result length: %d chars", resultLen)
	}

	duration := time.Since(startTime)
	s.opLog.Log(OpSearch, normRoot,
		fmt.Sprintf("search: %s", query),
		duration, true, "", LogEntry{IndexMs: indexMs, ApiMs: apiMs})

	return &SearchResult{
		Status:  "success",
		Message: "search completed",
		Output:  res.Formatted,
	}, nil
}

// Metrics returns basic in-memory counters.
func (s *Service) Metrics() map[string]any {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	return map[string]any{
		"index_runs":       s.indexRuns,
		"search_runs":      s.searchRuns,
		"last_index_time":  s.lastIndexTime.Format(time.RFC3339),
		"last_search_time": s.lastSearchTime.Format(time.RFC3339),
		"watch_projects":   len(s.watchers),
	}
}

// RecentLogs returns recent operation logs.
func (s *Service) RecentLogs(n int) []OpLog {
	return s.opLog.Recent(n)
}

// LogsSince returns logs since a given ID.
func (s *Service) LogsSince(afterID int64) []OpLog {
	return s.opLog.Since(afterID)
}

// StartWatching starts fsnotify watcher for a project (recursive) with debounce.
func (s *Service) StartWatching(projectRoot string) {
	root := s.normalizePath(projectRoot)

	// Check if this path is within an already watched project
	s.mu.Lock()
	for watchedRoot := range s.watchers {
		if s.isSubPath(watchedRoot, root) {
			s.mu.Unlock()
			s.logger.Debug("path already covered by existing watcher, skipping",
				logging.String("project", root),
				logging.String("existing_watcher", watchedRoot),
			)
			return
		}
	}
	if _, ok := s.watchers[root]; ok {
		s.mu.Unlock()
		s.logger.Debug("watcher already exists, skipping",
			logging.String("project", root),
		)
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		s.mu.Unlock()
		s.logger.Error("failed to create watcher",
			logging.String("project", root),
			logging.Error(err),
		)
		return
	}
	s.watchers[root] = w
	s.mu.Unlock()

	s.logger.Info("starting file watcher",
		logging.String("project", root),
	)

	// add all directories
	var watchedDirs int
	_ = filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(projectRoot, path)
		if relErr == nil {
			rel = strings.ReplaceAll(rel, "\\", "/")
			if rel != "." && s.shouldSkip(projectRoot, rel, true) {
				return fs.SkipDir
			}
		}
		if addErr := w.Add(path); addErr != nil {
			s.logger.Debug("failed to add directory to watcher",
				logging.String("path", path),
				logging.Error(addErr),
			)
		} else {
			watchedDirs++
		}
		return nil
	})

	s.logger.Info("watcher initialized",
		logging.String("project", root),
		logging.Int("watched_dirs", watchedDirs),
	)

	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				rel, relErr := filepath.Rel(projectRoot, ev.Name)
				if relErr != nil {
					continue
				}
				rel = strings.ReplaceAll(rel, "\\", "/")
				if rel == "." || strings.HasPrefix(rel, "../") || rel == ".." {
					continue
				}

				s.logger.Debug("fsnotify event",
					logging.String("op", ev.Op.String()),
					logging.String("path", ev.Name),
					logging.String("rel", rel),
				)

				if ev.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						relDir := rel
						if relDir != "." && s.shouldSkip(projectRoot, relDir, true) {
							continue
						}
						_ = filepath.WalkDir(ev.Name, func(path string, d fs.DirEntry, err error) error {
							if err != nil {
								return nil
							}
							if !d.IsDir() {
								return nil
							}
							r, rerr := filepath.Rel(projectRoot, path)
							if rerr == nil {
								r = strings.ReplaceAll(r, "\\", "/")
								if r != "." && s.shouldSkip(projectRoot, r, true) {
									return fs.SkipDir
								}
							}
							_ = w.Add(path)
							return nil
						})
						continue
					}
				}

				if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
					s.scheduleFileChange(root, projectRoot, rel, true, 600*time.Millisecond)
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
					s.logger.Debug("scheduling file change",
						logging.String("project", root),
						logging.String("rel", rel),
					)
					s.scheduleFileChange(root, projectRoot, rel, false, 600*time.Millisecond)
				}
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				s.logger.Warn("watcher error", logging.Error(err))
			}
		}
	}()
}

func (s *Service) scheduleFileChange(key string, projectRoot string, relPath string, isDelete bool, delay time.Duration) {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	s.mu.Lock()
	pc := s.pending[key]
	if pc == nil {
		pc = &pendingChanges{
			projectRoot: projectRoot,
			upsert:      make(map[string]struct{}),
			delete:      make(map[string]struct{}),
		}
		s.pending[key] = pc
	}
	pc.projectRoot = projectRoot
	if isDelete {
		pc.delete[relPath] = struct{}{}
		delete(pc.upsert, relPath)
	} else {
		pc.upsert[relPath] = struct{}{}
		delete(pc.delete, relPath)
	}
	if t, ok := s.debounceTimers[key]; ok {
		t.Stop()
	}
	s.debounceTimers[key] = time.AfterFunc(delay, func() {
		s.processPendingChanges(key)
	})
	s.mu.Unlock()
}

func (s *Service) processPendingChanges(key string) {
	s.mu.Lock()
	pc := s.pending[key]
	delete(s.pending, key)
	delete(s.debounceTimers, key)
	s.mu.Unlock()
	if pc == nil {
		return
	}

	upserts := make([]string, 0, len(pc.upsert))
	for p := range pc.upsert {
		upserts = append(upserts, p)
	}
	deletes := make([]string, 0, len(pc.delete))
	for p := range pc.delete {
		deletes = append(deletes, p)
	}

	s.metricsMu.Lock()
	s.indexRuns++
	s.lastIndexTime = time.Now()
	s.metricsMu.Unlock()

	if err := s.ApplyFileChanges(pc.projectRoot, upserts, deletes); err != nil {
		s.logger.Warn("apply file changes failed", logging.Error(err))
	}
}

func (s *Service) flushPendingChanges(key string) (pendingUpserts int) {
	s.mu.Lock()
	if t, ok := s.debounceTimers[key]; ok {
		t.Stop()
		delete(s.debounceTimers, key)
	}
	pc := s.pending[key]
	delete(s.pending, key)
	s.mu.Unlock()
	if pc == nil {
		return 0
	}

	pendingUpserts = len(pc.upsert)

	upserts := make([]string, 0, len(pc.upsert))
	for p := range pc.upsert {
		upserts = append(upserts, p)
	}
	deletes := make([]string, 0, len(pc.delete))
	for p := range pc.delete {
		deletes = append(deletes, p)
	}
	if err := s.ApplyFileChanges(pc.projectRoot, upserts, deletes); err != nil {
		s.logger.Warn("apply file changes failed", logging.Error(err))
	}

	return pendingUpserts
}

// StopWatching stops watcher for a project.
func (s *Service) StopWatching(projectRoot string) {
	root := s.normalizePath(projectRoot)
	s.mu.Lock()
	w := s.watchers[root]
	delete(s.watchers, root)
	delete(s.pending, root)
	if t, ok := s.debounceTimers[root]; ok {
		t.Stop()
		delete(s.debounceTimers, root)
	}
	s.mu.Unlock()
	if w != nil {
		_ = w.Close()
	}
}

// StopAll stops all watchers and flushes pending changes.
func (s *Service) StopAll() {
	s.mu.Lock()
	roots := make([]string, 0, len(s.watchers))
	for root := range s.watchers {
		roots = append(roots, root)
	}
	s.mu.Unlock()

	for _, root := range roots {
		s.flushPendingChanges(root)
		s.StopWatching(root)
	}
}
