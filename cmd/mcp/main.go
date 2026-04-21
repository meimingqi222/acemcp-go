package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/meimingqi222/acemcp-go/internal/logging"
	"github.com/meimingqi222/acemcp-go/internal/version"
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

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	ClientInfo      clientInfo     `json:"clientInfo"`
	Capabilities    map[string]any `json:"capabilities"`
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

type transportMode int

const (
	transportPlain transportMode = iota
	transportFramed
)

type requestDispatcher func(rpcRequest) rpcResponse

type responseWriter struct {
	mu        sync.Mutex
	writer    *bufio.Writer
	enc       *json.Encoder
	transport transportMode
}

func newResponseWriter(w io.Writer, transport transportMode) *responseWriter {
	bw := bufio.NewWriter(w)
	rw := &responseWriter{
		writer:    bw,
		transport: transport,
	}
	if transport == transportPlain {
		rw.enc = json.NewEncoder(bw)
	}
	return rw
}

func (w *responseWriter) Write(resp rpcResponse) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var err error
	if w.transport == transportFramed {
		err = writeFramedResponse(w.writer, resp)
	} else {
		err = w.enc.Encode(resp)
	}
	if err != nil {
		return err
	}
	return w.writer.Flush()
}

var ensureDaemonMu sync.Mutex

func main() {
	dataDir := flag.String("data", "", "Data directory (passed to daemon; defaults to ~/.acemcp/data)")
	logLevel := flag.String("log-level", "warn", "Log level: debug|info|warn|error (recommend warn/error for MCP)")
	daemonAddr := flag.String("daemon-addr", "127.0.0.1:7033", "Daemon JSON-RPC listen address")
	daemonHTTP := flag.String("daemon-http", "127.0.0.1:7034", "Daemon HTTP management address")
	daemonLogLevel := flag.String("daemon-log-level", "warn", "Daemon log level: debug|info|warn|error")
	daemonPath := flag.String("daemon-path", "", "Path to daemon executable (cmd/daemon). If empty, tries to find it next to this binary or in PATH")
	daemonStartTimeout := flag.Duration("daemon-start-timeout", 5*time.Second, "Timeout waiting for daemon to start")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		fmt.Printf("acemcp-go %s (commit: %s, built: %s)\n", version.Version, version.GitCommit, version.BuildTime)
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) > 0 {
		runCLI(args, *daemonAddr, *daemonHTTP, *daemonLogLevel, *daemonPath, *dataDir, *daemonStartTimeout, *logLevel)
		return
	}

	runMCP(*daemonAddr, *daemonHTTP, *daemonLogLevel, *daemonPath, *dataDir, *daemonStartTimeout, *logLevel)
}

func runCLI(args []string, daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir string, startTimeout time.Duration, logLevel string) {
	logger, err := logging.NewLogger(logLevel)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "init logger:", err)
		os.Exit(1)
	}
	defer logger.Sync()

	cmd := args[0]
	switch cmd {
	case "search":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: acemcp-go search <project_root> <query>")
			fmt.Fprintln(os.Stderr, "Example: acemcp-go search /path/to/project \"How does authentication work?\"")
			os.Exit(1)
		}
		projectRoot := args[1]
		query := strings.Join(args[2:], " ")

		if err := ensureDaemon(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "Failed to start daemon:", err)
			os.Exit(1)
		}

		raw, err := callDaemon(daemonAddr, "SearchContext", map[string]any{"project_root_path": projectRoot, "query": query})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Search failed:", err)
			os.Exit(1)
		}
		var sr daemonSearchResult
		_ = json.Unmarshal(raw, &sr)
		if sr.Output != "" {
			fmt.Println(sr.Output)
		} else if sr.Message != "" {
			fmt.Println(sr.Message)
		} else {
			fmt.Println(string(raw))
		}

	case "index":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: acemcp-go index <project_root>")
			os.Exit(1)
		}
		projectRoot := args[1]

		if err := ensureDaemon(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "Failed to start daemon:", err)
			os.Exit(1)
		}

		raw, err := callDaemon(daemonAddr, "IndexProject", map[string]any{"project_root_path": projectRoot})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Index failed:", err)
			os.Exit(1)
		}
		fmt.Println(string(raw))

	case "status":
		if err := ensureDaemon(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout); err != nil {
			fmt.Fprintln(os.Stderr, "Daemon not running:", err)
			os.Exit(1)
		}
		raw, err := callDaemon(daemonAddr, "Status", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Status failed:", err)
			os.Exit(1)
		}
		fmt.Println(string(raw))

	case "help", "--help", "-h":
		printCLIHelp()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printCLIHelp()
		os.Exit(1)
	}
}

func printCLIHelp() {
	fmt.Println("acemcp-go - AI-powered code search and indexing tool")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  acemcp-go                           Start MCP server (for IDE integration)")
	fmt.Println("  acemcp-go search <project> <query>  Search code using natural language")
	fmt.Println("  acemcp-go index <project>           Index a project for searching")
	fmt.Println("  acemcp-go status                    Show daemon status")
	fmt.Println("  acemcp-go --version                 Show version")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  acemcp-go search /home/user/myapp \"Where is authentication handled?\"")
	fmt.Println("  acemcp-go index /home/user/myapp")
	fmt.Println()
	fmt.Println("MCP Server Mode:")
	fmt.Println("  When run without arguments, starts as an MCP server for IDE integration.")
	fmt.Println("  Configure in Cursor settings.json:")
	fmt.Println(`    {"mcpServers": {"acemcp": {"command": "acemcp-go"}}}`)
}

func runMCP(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir string, startTimeout time.Duration, logLevel string) {
	logger, err := logging.NewLogger(logLevel)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "init logger:", err)
		os.Exit(1)
	}
	defer logger.Sync() //nolint:errcheck
	logger.Info("acemcp-go mcp proxy starting",
		logging.String("version", version.Version),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dispatcher := func(req rpcRequest) rpcResponse {
		return dispatch(req, daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout)
	}
	if err := serveMCP(ctx, os.Stdin, os.Stdout, logger, dispatcher); err != nil {
		if isBrokenPipeError(err) {
			logger.Info("client disconnected (broken pipe), shutting down gracefully")
			return
		}
		if !errors.Is(err, io.EOF) {
			logger.Error("mcp server stopped with read error", logging.Error(err))
		}
	}
}

func serveMCP(ctx context.Context, input io.Reader, output io.Writer, logger *logging.Logger, dispatcher requestDispatcher) error {
	stdin := bufio.NewReader(input)
	transport := detectTransport(stdin)
	writer := newResponseWriter(output, transport)

	var dec *json.Decoder
	if transport == transportPlain {
		dec = json.NewDecoder(stdin)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var req rpcRequest
		var err error
		if transport == transportFramed {
			err = readFramedRequest(stdin, &req)
		} else {
			err = dec.Decode(&req)
		}
		if err != nil {
			return err
		}

		if req.ID == nil && req.Method == "exit" {
			return nil
		}

		if req.ID == nil {
			switch req.Method {
			case "initialized":
			default:
			}
			continue
		}

		reqCopy := req
		go func() {
			resp := dispatcher(reqCopy)
			if err := writer.Write(resp); err != nil {
				if isBrokenPipeError(err) {
					logger.Info("client disconnected (broken pipe), shutting down gracefully")
					return
				}
				logger.Error("write response error", logging.Error(err))
			}
		}()
	}
}

func detectTransport(reader *bufio.Reader) transportMode {
	peek, err := reader.Peek(1)
	if err != nil || len(peek) == 0 {
		return transportPlain
	}
	if peek[0] == '{' || peek[0] == '[' {
		return transportPlain
	}
	return transportFramed
}

func readFramedRequest(reader *bufio.Reader, req *rpcRequest) error {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return fmt.Errorf("invalid Content-Length")
			}
			contentLength = n
		}
	}

	if contentLength <= 0 {
		return fmt.Errorf("missing Content-Length")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return err
	}

	return json.Unmarshal(body, req)
}

func writeFramedResponse(writer *bufio.Writer, resp rpcResponse) error {
	body, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		return err
	}
	return nil
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
		// Eagerly start daemon so it's ready before the first tool call.
		// Without this, the first tools/call triggers daemon startup (1-5s),
		// which can cause the agent's initialization timeout to fire and kill
		// the MCP proxy before the tool response arrives.
		// We ignore errors here; tools/call will surface them when invoked.
		_ = ensureDaemon(daemonAddr, daemonHTTP, daemonLogLevel, daemonPath, dataDir, startTimeout)
		// Support protocol version negotiation
		// Codex rmcp_client expects 2024-11-05
		pv := p.ProtocolVersion
		switch pv {
		case "2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25":
			// Use client's requested version
		default:
			// Default to 2024-11-05 for maximum compatibility
			pv = "2024-11-05"
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

	// Note: "initialized" is a notification (no ID) and is handled in the main loop

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
					Description: "Search for relevant code context using semantic search within a specific project. This tool automatically indexes the project (if not already indexed) and finds code snippets that match your natural language query. Ideal for locating function implementations, understanding business logic, finding specific code patterns, or analyzing code structure.\n\nBEHAVIOR:\n- When you pass a PROJECT ROOT (e.g., 'C:/project' or '/home/dev/project'), it searches ONLY that project.\n- When you pass a PARENT DIRECTORY containing multiple sub-projects (e.g., 'C:/workspace' containing 'project-a', 'project-b'), it automatically:\n  1. Finds all indexed sub-projects\n  2. Indexes any unindexed sub-projects\n  3. Searches across ALL sub-projects together\n\nEach path is indexed independently - searching a sub-project will NOT include results from sibling projects.",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"project_root_path": map[string]any{
								"type":        "string",
								"description": "Absolute path to search. For a single project: pass its root directory. For a workspace with multiple projects: pass the parent directory to search across all sub-projects. Examples: 'C:/Users/myapp' or '/home/dev/myproject'.",
							},
							"query": map[string]any{
								"type":        "string",
								"description": "A semantic search query in natural language. MUST be a complete descriptive sentence, NOT a list of keywords. Good examples: 'How does the authentication middleware validate JWT tokens?', 'Find the function that calculates order totals including discounts', 'Where is the database connection pool configured?'. Bad examples: 'auth JWT token', 'order total discount', 'db connection'. The search uses embeddings to find semantically similar code, so descriptive questions work best.",
							},
						},
						"required": []string{"project_root_path", "query"},
					},
				},
			},
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
	ensureDaemonMu.Lock()
	defer ensureDaemonMu.Unlock()

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

// isBrokenPipeError checks if an error is a broken pipe error
func isBrokenPipeError(err error) bool {
	if err == nil {
		return false
	}
	// Check for specific error strings that indicate broken pipe
	// This is a best-effort check since Go doesn't export EPIPE directly
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "write: broken pipe") ||
		strings.Contains(errStr, "EPIPE")
}
