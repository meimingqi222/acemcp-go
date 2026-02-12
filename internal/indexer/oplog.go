package indexer

import (
	"fmt"
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
	OpApply   OpType = "apply"
	OpCollect OpType = "collect"
	OpError   OpType = "error"
	OpDebug   OpType = "debug"
)

// LogLevel represents severity level.
type LogLevel string

const (
	LevelDebug LogLevel = "debug"
	LevelInfo  LogLevel = "info"
	LevelWarn  LogLevel = "warn"
	LevelError LogLevel = "error"
)

// OpLog represents a single operation log entry.
type OpLog struct {
	ID        int64     `json:"id"`
	Time      time.Time `json:"time"`
	Level     LogLevel  `json:"level"`
	Type      OpType    `json:"type"`
	Project   string    `json:"project,omitempty"`
	Message   string    `json:"message"`
	Duration  int64     `json:"duration_ms,omitempty"`
	IndexMs   int64     `json:"index_ms,omitempty"`
	ApiMs     int64     `json:"api_ms,omitempty"`
	Success   bool      `json:"success"`
	Details   string    `json:"details,omitempty"`
	Count     int       `json:"count,omitempty"`
	Extra     string    `json:"extra,omitempty"`
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
		maxLogs = 500
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
	level := LevelInfo
	if !success {
		level = LevelError
	}
	l.log(level, opType, project, message, duration, success, details, 0, "", opts...)
}

func (l *OpLogger) log(level LogLevel, opType OpType, project, message string, duration time.Duration, success bool, details string, count int, extra string, opts ...LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := OpLog{
		ID:       l.nextID,
		Time:     time.Now(),
		Level:    level,
		Type:     opType,
		Project:  project,
		Message:  message,
		Duration: duration.Milliseconds(),
		Success:  success,
		Details:  details,
		Count:    count,
		Extra:    extra,
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

// Info logs an info level message.
func (l *OpLogger) Info(opType OpType, project, message string) {
	l.log(LevelInfo, opType, project, message, 0, true, "", 0, "")
}

// Infof logs a formatted info message.
func (l *OpLogger) Infof(opType OpType, project, format string, args ...any) {
	l.log(LevelInfo, opType, project, fmt.Sprintf(format, args...), 0, true, "", 0, "")
}

// Warn logs a warning level message.
func (l *OpLogger) Warn(opType OpType, project, message string, details string) {
	l.log(LevelWarn, opType, project, message, 0, false, details, 0, "")
}

// Warnf logs a formatted warning message.
func (l *OpLogger) Warnf(opType OpType, project, format string, args ...any) {
	l.log(LevelWarn, opType, project, fmt.Sprintf(format, args...), 0, false, "", 0, "")
}

// Error logs an error level message.
func (l *OpLogger) Error(opType OpType, project, message string, details string) {
	l.log(LevelError, opType, project, message, 0, false, details, 0, "")
}

// Errorf logs a formatted error message.
func (l *OpLogger) Errorf(opType OpType, project, format string, args ...any) {
	l.log(LevelError, opType, project, fmt.Sprintf(format, args...), 0, false, "", 0, "")
}

// Debug logs a debug level message.
func (l *OpLogger) Debug(opType OpType, project, message string) {
	l.log(LevelDebug, opType, project, message, 0, true, "", 0, "")
}

// LogWithCount logs with a count field (e.g., number of files).
func (l *OpLogger) LogWithCount(level LogLevel, opType OpType, project, message string, count int) {
	l.log(level, opType, project, message, 0, level != LevelError, "", count, "")
}

// LogUpload logs upload operations with details.
func (l *OpLogger) LogUpload(project string, success bool, count int, details string) {
	level := LevelInfo
	if !success {
		level = LevelWarn
	}
	msg := fmt.Sprintf("uploaded %d blobs", count)
	if !success {
		msg = fmt.Sprintf("upload failed (%d blobs)", count)
	}
	l.log(level, OpUpload, project, msg, 0, success, details, count, "")
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

// RecentByLevel returns recent logs filtered by minimum level.
func (l *OpLogger) RecentByLevel(n int, minLevel LogLevel) []OpLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	levelOrder := map[LogLevel]int{
		LevelDebug: 0,
		LevelInfo:  1,
		LevelWarn:  2,
		LevelError: 3,
	}
	minOrd := levelOrder[minLevel]

	var result []OpLog
	for i := len(l.logs) - 1; i >= 0 && len(result) < n; i-- {
		if levelOrder[l.logs[i].Level] >= minOrd {
			result = append(result, l.logs[i])
		}
	}
	return result
}

// Since returns logs since a given ID (for incremental fetching).
// Returns logs in reverse order (newest first), same as Recent().
func (l *OpLogger) Since(afterID int64) []OpLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Collect matching logs
	var result []OpLog
	for _, log := range l.logs {
		if log.ID > afterID {
			result = append(result, log)
		}
	}

	// Reverse to get newest first (same as Recent())
	if len(result) <= 1 {
		return result
	}

	// Reverse in place
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// Clear clears all logs.
func (l *OpLogger) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = l.logs[:0]
}
