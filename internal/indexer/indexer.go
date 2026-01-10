package indexer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	gitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/yourorg/acemcp-go/internal/config"
	"github.com/yourorg/acemcp-go/internal/logging"
)

type blob struct {
	Path    string `json:"path"`
	Content string `json:"content"`
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

	metricsMu      sync.Mutex
	indexRuns      int
	searchRuns     int
	lastIndexTime  time.Time
	lastSearchTime time.Time
}

func New(cfg *config.Config, logger *logging.Logger) *Service {
	_ = os.MkdirAll(cfg.DataDir, 0o755)
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
		opLog:          NewOpLogger(200),
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

type filesIndex map[string]map[string][]string // project_root -> file_path -> blob_hashes

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
	out := filesIndex{}
	b, err := os.ReadFile(s.filesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) saveFilesIndex(idx filesIndex) error {
	if err := os.MkdirAll(filepath.Dir(s.filesPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filesPath, b, 0o644)
}

// ListFailed returns project -> failed blobs.
func (s *Service) ListFailed() (map[string][]failedBlob, error) {
	return s.loadFailed()
}

func (s *Service) normalizePath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return strings.ReplaceAll(path, "\\", "/")
	}
	return strings.ReplaceAll(abs, "\\", "/")
}

func (s *Service) collectBlobs(root string) ([]blob, error) {
	var blobs []blob
	maxLines := s.cfg.MaxLinesPerBlob
	if maxLines <= 0 {
		maxLines = 800
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = strings.ReplaceAll(rel, "\\", "/")
		// Skip ignored directories early
		if d.IsDir() {
			if rel != "." && s.shouldSkip(root, rel, true) {
				return fs.SkipDir
			}
			return nil
		}
		if s.shouldSkip(root, rel, false) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		allowed := false
		for _, e := range s.cfg.TextExtensions {
			if strings.ToLower(e) == ext {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			s.logger.Warn("read file failed", logging.String("file", rel), logging.Error(err))
			return nil
		}
		content := string(data)
		lines := strings.Split(content, "\n")
		if len(lines) <= maxLines {
			blobs = append(blobs, blob{
				Path:    rel,
				Content: content,
			})
			return nil
		}

		// split into chunks
		total := len(lines)
		chunks := (total + maxLines - 1) / maxLines
		for i := 0; i < chunks; i++ {
			start := i * maxLines
			end := start + maxLines
			if end > total {
				end = total
			}
			chunkContent := strings.Join(lines[start:end], "\n")
			chunkPath := fmt.Sprintf("%s#chunk%dof%d", rel, i+1, chunks)
			blobs = append(blobs, blob{
				Path:    chunkPath,
				Content: chunkContent,
			})
		}
		s.logger.Info("split large file",
			logging.String("file", rel),
			logging.Int("lines", total),
			logging.Int("chunks", chunks),
			logging.Int("max_lines", maxLines),
		)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func hashBlob(b blob) string {
	h := sha256.New()
	h.Write([]byte(b.Path))
	h.Write([]byte(b.Content))
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Service) loadProjects() (map[string][]string, error) {
	projects := map[string][]string{}
	b, err := os.ReadFile(s.projectsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return projects, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return projects, nil
	}
	if err := json.Unmarshal(b, &projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (s *Service) saveProjects(projects map[string][]string) error {
	if err := os.MkdirAll(filepath.Dir(s.projectsPath), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.projectsPath, data, 0o644)
}

func (s *Service) loadFailed() (map[string][]failedBlob, error) {
	result := map[string][]failedBlob{}
	b, err := os.ReadFile(s.failedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) saveFailed(data map[string][]failedBlob) error {
	if err := os.MkdirAll(filepath.Dir(s.failedPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.failedPath, b, 0o644)
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

func (s *Service) uploadBlobs(blobs []blob) ([]string, error) {
	payload := map[string]any{
		"blobs": blobs,
	}
	body, _ := json.Marshal(payload)
	u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/batch-upload")
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}

	req, err := http.NewRequest("POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = s.client.Do(req)
		if err == nil {
			break
		}
		s.logger.Warn("upload request failed, retrying",
			logging.Error(err),
			logging.Int("attempt", attempt+1),
		)
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upload failed: %s", string(b))
	}
	var res struct {
		BlobNames []string `json:"blob_names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.BlobNames, nil
}

// IndexProject collects files, uploads new blobs, and updates project state.
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

	blobs, err := s.collectBlobs(projectRoot)
	if err != nil {
		return nil, err
	}
	if len(blobs) == 0 {
		return &IndexResult{Status: "error", Message: "no text files found"}, nil
	}
	projects, err := s.loadProjects()
	if err != nil {
		return nil, err
	}
	filesIdx, err := s.loadFilesIndex()
	if err != nil {
		return nil, err
	}
	prevFiles := filesIdx[normRoot]
	if prevFiles == nil {
		prevFiles = map[string][]string{}
	}
	// Build current file->hashes mapping and current hash set
	currentFiles := map[string][]string{}
	currentHashes := map[string]struct{}{}
	for _, b := range blobs {
		h := hashBlob(b)
		base := b.Path
		if i := strings.Index(base, "#chunk"); i >= 0 {
			base = base[:i]
		}
		currentFiles[base] = append(currentFiles[base], h)
		currentHashes[h] = struct{}{}
	}

	// Determine removed hashes based on previous snapshot
	removed := make([]string, 0)
	for _, hs := range prevFiles {
		for _, h := range hs {
			if _, ok := currentHashes[h]; !ok {
				removed = append(removed, h)
			}
		}
	}

	// Existing hashes (present in previous projects and still present in current snapshot)
	existing := map[string]struct{}{}
	for _, h := range projects[normRoot] {
		if _, ok := currentHashes[h]; ok {
			existing[h] = struct{}{}
		}
	}

	// Also remove deleted hashes from failed list
	if len(removed) > 0 {
		s.removeFailed(normRoot, removed)
	}

	var blobsToUpload []blob
	var blobsToUploadHashes []string
	for _, b := range blobs {
		h := hashBlob(b)
		if _, ok := existing[h]; ok {
			continue
		}
		blobsToUpload = append(blobsToUpload, b)
		blobsToUploadHashes = append(blobsToUploadHashes, h)
	}

	var uploaded []string
	if len(blobsToUpload) > 0 {
		batchSize := s.cfg.BatchSize
		if batchSize <= 0 {
			batchSize = 10
		}
		for i := 0; i < len(blobsToUpload); i += batchSize {
			end := i + batchSize
			if end > len(blobsToUpload) {
				end = len(blobsToUpload)
			}
			// Try batch upload, fallback to per-blob on failure
			_, err := s.uploadBlobs(blobsToUpload[i:end])
			if err == nil {
				uploaded = append(uploaded, blobsToUploadHashes[i:end]...)
				// clear failed marks for successful uploads
				s.removeFailed(normRoot, blobsToUploadHashes[i:end])
				continue
			}
			// per-blob fallback
			for j := i; j < end; j++ {
				_, berr := s.uploadBlobs([]blob{blobsToUpload[j]})
				if berr != nil {
					s.addFailed(normRoot, blobsToUploadHashes[j], blobsToUpload[j].Path, berr.Error())
					continue
				}
				uploaded = append(uploaded, blobsToUploadHashes[j])
				s.removeFailed(normRoot, []string{blobsToUploadHashes[j]})
			}
		}
	}

	// Update project blobs: existing (still present) + newly uploaded
	newProject := make([]string, 0, len(existing)+len(uploaded))
	for h := range existing {
		newProject = append(newProject, h)
	}
	newProject = append(newProject, uploaded...)
	projects[normRoot] = newProject

	// persist current file snapshot
	filesIdx[normRoot] = currentFiles
	if err := s.saveFilesIndex(filesIdx); err != nil {
		return nil, err
	}
	if err := s.saveProjects(projects); err != nil {
		return nil, err
	}

	duration := time.Since(startTime)
	s.opLog.Log(OpIndex, normRoot,
		fmt.Sprintf("indexed %d blobs (new %d)", len(projects[normRoot]), len(uploaded)),
		duration, true, "")

	return &IndexResult{
		Status:     "success",
		Message:    fmt.Sprintf("indexed %d total blobs (new %d)", len(projects[normRoot]), len(uploaded)),
		NewBlobs:   len(uploaded),
		TotalBlobs: len(projects[normRoot]),
	}, nil
}

func (s *Service) isAllowedTextExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range s.cfg.TextExtensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
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
	s.opMu.Lock()
	defer s.opMu.Unlock()

	normRoot := s.normalizePath(projectRoot)
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
		prevFiles = map[string][]string{}
	}
	projectSet := make(map[string]struct{}, len(projects[normRoot]))
	for _, h := range projects[normRoot] {
		projectSet[h] = struct{}{}
	}

	removedHashes := make([]string, 0)
	for _, rel := range deletes {
		rel = strings.ReplaceAll(rel, "\\", "/")
		old := prevFiles[rel]
		if len(old) == 0 {
			delete(prevFiles, rel)
			continue
		}
		for _, h := range old {
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
			old := prevFiles[rel]
			if len(old) > 0 {
				for _, h := range old {
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
			old := prevFiles[rel]
			if len(old) > 0 {
				for _, h := range old {
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

		old := prevFiles[rel]
		if len(old) > 0 {
			for _, h := range old {
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

		prevFiles[rel] = newHashes
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
	return nil
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
	s.StartWatching(projectRoot)

	s.opMu.Lock()
	projects, err := s.loadProjects()
	s.opMu.Unlock()
	if err != nil {
		return nil, err
	}
	indexStart := time.Now()
	if len(projects[normRoot]) == 0 {
		if _, err := s.IndexProject(projectRoot); err != nil {
			return nil, err
		}
	} else {
		s.flushPendingChanges(normRoot)
	}
	indexMs = time.Since(indexStart).Milliseconds()

	s.opMu.Lock()
	projects, err = s.loadProjects()
	s.opMu.Unlock()
	if err != nil {
		return nil, err
	}
	blobNames := projects[normRoot]
	if len(blobNames) == 0 {
		return nil, fmt.Errorf("no blobs found for project")
	}
	failedSet := s.getFailedHashes(normRoot)
	if len(failedSet) > 0 {
		filtered := blobNames[:0]
		for _, h := range blobNames {
			if _, bad := failedSet[h]; bad {
				continue
			}
			filtered = append(filtered, h)
		}
		blobNames = filtered
	}
	if len(blobNames) == 0 {
		return nil, fmt.Errorf("no valid blobs available for search")
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
	var resp *http.Response
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err = s.client.Do(req)
		if err == nil {
			break
		}
		s.logger.Warn("search request failed, retrying",
			logging.Error(err),
			logging.Int("attempt", attempt+1),
		)
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search failed: %s", string(b))
	}

	var res struct {
		Formatted string `json:"formatted_retrieval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	apiMs := time.Since(apiStart).Milliseconds()
	if res.Formatted == "" {
		res.Formatted = "No relevant code context found."
	}

	duration := time.Since(startTime)
	s.opLog.Log(OpSearch, normRoot,
		fmt.Sprintf("search: %s", truncateQuery(query, 50)),
		duration, true, "", LogEntry{IndexMs: indexMs, ApiMs: apiMs})

	return &SearchResult{
		Status:  "success",
		Message: "search completed",
		Output:  res.Formatted,
	}, nil
}

func truncateQuery(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
	s.mu.Lock()
	if _, ok := s.watchers[root]; ok {
		s.mu.Unlock()
		return
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		s.mu.Unlock()
		return
	}
	s.watchers[root] = w
	s.mu.Unlock()

	// add all directories
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
		_ = w.Add(path)
		return nil
	})

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

func (s *Service) flushPendingChanges(key string) {
	s.mu.Lock()
	if t, ok := s.debounceTimers[key]; ok {
		t.Stop()
		delete(s.debounceTimers, key)
	}
	pc := s.pending[key]
	delete(s.pending, key)
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
	if err := s.ApplyFileChanges(pc.projectRoot, upserts, deletes); err != nil {
		s.logger.Warn("apply file changes failed", logging.Error(err))
	}
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
