package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meimingqi222/acemcp-go/internal/logging"
)

func TestServeMCPHandlesConcurrentRequests(t *testing.T) {
	logger, err := logging.NewLogger("error")
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer outR.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveMCP(ctx, inR, outW, logger, func(req rpcRequest) rpcResponse {
			if id, _ := req.ID.(string); id == "slow" {
				time.Sleep(250 * time.Millisecond)
			}
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"ok": true},
			}
		})
		_ = outW.Close()
	}()

	writeFramedTestRequest(t, inW, rpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      "slow",
		Params:  mustMarshal(map[string]any{"name": "search_context"}),
	})
	writeFramedTestRequest(t, inW, rpcRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      "fast",
		Params:  mustMarshal(map[string]any{"name": "search_context"}),
	})

	reader := bufio.NewReader(outR)

	first := readFramedTestResponse(t, reader, 150*time.Millisecond)
	if got, _ := first.ID.(string); got != "fast" {
		t.Fatalf("expected fast response first, got %v", first.ID)
	}

	second := readFramedTestResponse(t, reader, 500*time.Millisecond)
	if got, _ := second.ID.(string); got != "slow" {
		t.Fatalf("expected slow response second, got %v", second.ID)
	}

	_ = inW.Close()

	select {
	case err := <-errCh:
		if err != nil && err != io.EOF {
			t.Fatalf("serveMCP returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for serveMCP to exit")
	}
}

func writeFramedTestRequest(t *testing.T, w io.Writer, req rpcRequest) {
	t.Helper()

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if _, err := io.WriteString(w, "Content-Length: "); err != nil {
		t.Fatalf("write header prefix: %v", err)
	}
	if _, err := io.WriteString(w, strconv.Itoa(len(body))); err != nil {
		t.Fatalf("write header length: %v", err)
	}
	if _, err := io.WriteString(w, "\r\n\r\n"); err != nil {
		t.Fatalf("write header suffix: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write body: %v", err)
	}
}

func readFramedTestResponse(t *testing.T, r *bufio.Reader, timeout time.Duration) rpcResponse {
	t.Helper()

	type result struct {
		resp rpcResponse
		err  error
	}

	ch := make(chan result, 1)
	go func() {
		var contentLength int
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				ch <- result{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			key, value, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(key, "Content-Length") {
				n, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					ch <- result{err: err}
					return
				}
				contentLength = n
			}
		}

		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			ch <- result{err: err}
			return
		}

		var resp rpcResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{resp: resp}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read framed response: %v", res.err)
		}
		return res.resp
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for response after %v", timeout)
		return rpcResponse{}
	}
}
