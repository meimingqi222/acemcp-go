package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yourorg/acemcp-go/internal/logging"
	"github.com/yourorg/acemcp-go/internal/version"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
	ID      any       `json:"id,omitempty"`
}

type daemonResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      any             `json:"id,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type toolsListParams struct {
	Cursor *string `json:"cursor,omitempty"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type daemonSearchResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Output  string `json:"output,omitempty"`
}

func main() {
	dataDir := flag.String("data", "", "Data directory (passed to daemon; defaults to ~/.acemcp/data)")
	logLevel := flag.String("log-level", "warn", "Log level: debug|info|warn|error (recommend warn/error for MCP)")
	daemonAddr := flag.String("daemon-addr", "127.0.0.1:7033", "Daemon JSON-RPC listen address")
	daemonHTTP := flag.String("daemon-http", "127.0.0.1:7034", "Daemon HTTP management address")
	daemonLogLevel := flag.String("daemon-log-level", "warn", "Daemon log level: debug|info|warn|error")
	daemonPath := flag.String("daemon-path", "", "Path to daemon executable (cmd/daemon). If empty, tries to find it next to this binary or in PATH")
	daemonStartTimeout := flag.Duration("daemon-start-timeout", 5*time.Second, "Timeout waiting for daemon to start")
	flag.Parse()

	logger, err := logging.NewLogger(*logLevel)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "init logger:", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck
	logger.Info("acemcp-go mcp proxy starting")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	stdout := bufio.NewWriter(os.Stdout)
	enc := json.NewEncoder(stdout)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}

		if req.ID == nil && req.Method == "exit" {
			return
		}

		// Notifications have no id; per MCP we can ignore unknown notifications.
		if req.ID == nil {
			continue
		}

		resp := dispatch(req, *daemonAddr, *daemonHTTP, *daemonLogLevel, *daemonPath, *dataDir, *daemonStartTimeout)
		if err := enc.Encode(resp); err != nil {
			return
		}
		_ = stdout.Flush()
	}
}

func dispatch(req rpcRequest, daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir string, startTimeout time.Duration) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if req.JSONRPC != "2.0" {
		resp.Error = &rpcError{Code: -32600, Message: "invalid request: jsonrpc must be 2.0"}
		return resp
	}

	switch req.Method {
	case "initialize":
		var p initializeParams
		_ = json.Unmarshal(req.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = "2025-11-25"
		}
		resp.Result = map[string]any{
			"protocolVersion": pv,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    "acemcp-go",
				"version": version.Version,
			},
		}
		return resp

	case "initialized":
		resp.Result = map[string]any{}
		return resp

	case "shutdown":
		resp.Result = map[string]any{}
		return resp

	case "exit":
		resp.Result = map[string]any{}
		return resp

	case "tools/list":
		var p toolsListParams
		_ = json.Unmarshal(req.Params, &p)
		resp.Result = map[string]any{
			"tools": []tool{
				{
					Name:        "search_context",
					Description: "Search for relevant code context using semantic search within a specific project. This tool automatically indexes the project (if not already indexed) and finds code snippets that match your natural language query. Ideal for locating function implementations, understanding business logic, finding specific code patterns, or analyzing code structure.",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"project_root_path": map[string]any{
								"type":        "string",
								"description": "Absolute path to the project root directory. IMPORTANT: Always use forward slashes (/) as path separators, even on Windows. Example: 'C:/Users/username/project' or '/home/user/project'.",
							},
							"query": map[string]any{
								"type":        "string",
								"description": "A complete natural language sentence describing what code you want to find. Use full sentences like 'Find where the server handles user authentication' or 'Show me the code that processes file uploads'. Avoid keyword lists or comma-separated terms.",
							},
						},
						"required": []string{"project_root_path", "query"},
					},
				},
			},
			"nextCursor": nil,
		}
		return resp

	case "tools/call":
		var p toolsCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return resp
		}
		switch p.Name {
		case "search_context":
			projectRoot, _ := p.Arguments["project_root_path"].(string)
			query, _ := p.Arguments["query"].(string)
			if projectRoot == "" || query == "" {
				resp.Error = &rpcError{Code: -32602, Message: "invalid params: project_root_path and query required"}
				return resp
			}

			if err := ensureDaemon(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout); err != nil {
				resp.Result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": err.Error()}},
					"isError": true,
				}
				return resp
			}

			raw, callErr := callDaemon(daemonAddr, "SearchContext", map[string]any{"project_root_path": projectRoot, "query": query})
			if callErr != nil {
				resp.Result = map[string]any{
					"content": []map[string]any{{"type": "text", "text": callErr.Error()}},
					"isError": true,
				}
				return resp
			}
			var sr daemonSearchResult
			_ = json.Unmarshal(raw, &sr)
			text := sr.Output
			if text == "" {
				text = sr.Message
			}
			if text == "" {
				text = string(raw)
			}
			resp.Result = map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": false,
			}
			return resp
		default:
			resp.Error = &rpcError{Code: -32601, Message: "unknown tool"}
			return resp
		}

	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found"}
		return resp
	}
}

func callDaemon(addr string, method string, params map[string]any) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: mustMarshal(params), ID: 1}
	if err := enc.Encode(req); err != nil {
		return nil, err
	}
	var resp daemonResponse
	if err := dec.Decode(&resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp.Result, nil
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

func ensureDaemon(addr, httpAddr, daemonLogLevel, daemonPath, dataDir string, timeout time.Duration) error {
	if isPortOpen(addr, 150*time.Millisecond) {
		return nil
	}

	path := daemonPath
	if path == "" {
		path = findDaemonExecutable()
	}
	if path == "" {
		return fmt.Errorf("daemon not running and daemon executable not found; please set --daemon-path")
	}

	args := []string{"--listen", addr, "--http", httpAddr, "--log-level", daemonLogLevel}
	if dataDir != "" {
		args = append(args, "--data", dataDir)
	}

	cmd := exec.Command(path, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon failed: %w", err)
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isPortOpen(addr, 250*time.Millisecond) {
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within %s", timeout)
}

func isPortOpen(addr string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func findDaemonExecutable() string {
	// Prefer a sibling binary next to the current executable.
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		candidates := []string{
			"acemcp-go-daemon.exe",
			"acemcp-go-daemon",
			"acemcp-daemon.exe",
			"acemcp-daemon",
		}
		for _, name := range candidates {
			p := filepath.Join(dir, name)
			if fileExists(p) {
				return p
			}
		}
	}
	// Fallback: search in PATH.
	for _, name := range []string{"acemcp-go-daemon", "acemcp-daemon"} {
		p, err := exec.LookPath(name)
		if err == nil && p != "" {
			return p
		}
	}
	return ""
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}
