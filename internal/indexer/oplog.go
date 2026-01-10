package indexer

import (
	"sync"
	"time"
)

// OpType represents the type of operation.
type OpType string

const (
	OpIndex   OpType = "index"
	OpSearch  OpType = "search"
	OpWatch   OpType = "watch"
	OpUpload  OpType = "upload"
	OpApply   OpType = "apply" // apply file changes
)

// OpLog represents a single operation log entry.
type OpLog struct {
	ID        int64     `json:"id"`
	Time      time.Time `json:"time"`
	Type      OpType    `json:"type"`
	Project   string    `json:"project"`
	Message   string    `json:"message"`
	Duration  int64     `json:"duration_ms"`
	IndexMs   int64     `json:"index_ms"`
	ApiMs     int64     `json:"api_ms"`
	Success   bool      `json:"success"`
	Details   string    `json:"details,omitempty"`
}

// OpLogger stores recent operation logs in memory.
type OpLogger struct {
	mu      sync.RWMutex
	logs    []OpLog
	maxLogs int
	nextID  int64
}

// NewOpLogger creates a new operation logger.
func NewOpLogger(maxLogs int) *OpLogger {
	if maxLogs <= 0 {
		maxLogs = 100
	}
	return &OpLogger{
		logs:    make([]OpLog, 0, maxLogs),
		maxLogs: maxLogs,
		nextID:  1,
	}
}

// LogEntry is the optional parameters for logging.
type LogEntry struct {
	IndexMs int64
	ApiMs   int64
}

// Log adds a new operation log entry.
func (l *OpLogger) Log(opType OpType, project, message string, duration time.Duration, success bool, details string, opts ...LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := OpLog{
		ID:       l.nextID,
		Time:     time.Now(),
		Type:     opType,
		Project:  project,
		Message:  message,
		Duration: duration.Milliseconds(),
		Success:  success,
		Details:  details,
	}
	if len(opts) > 0 {
		entry.IndexMs = opts[0].IndexMs
		entry.ApiMs = opts[0].ApiMs
	}
	l.nextID++

	l.logs = append(l.logs, entry)
	if len(l.logs) > l.maxLogs {
		l.logs = l.logs[len(l.logs)-l.maxLogs:]
	}
}

// Recent returns the most recent n logs (newest first).
func (l *OpLogger) Recent(n int) []OpLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if n <= 0 || n > len(l.logs) {
		n = len(l.logs)
	}

	result := make([]OpLog, n)
	for i := 0; i < n; i++ {
		result[i] = l.logs[len(l.logs)-1-i]
	}
	return result
}

// Since returns logs since a given ID (for incremental fetching).
func (l *OpLogger) Since(afterID int64) []OpLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []OpLog
	for _, log := range l.logs {
		if log.ID > afterID {
			result = append(result, log)
		}
	}
	return result
}

// Clear clears all logs.
func (l *OpLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = l.logs[:0]
}
