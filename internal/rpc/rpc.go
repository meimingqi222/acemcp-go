package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/yourorg/acemcp-go/internal/logging"
)

// HandlerFunc handles a JSON-RPC method.
type HandlerFunc func(params json.RawMessage) (any, *Error)

// Server is a minimal JSON-RPC 2.0 over TCP server (line-delimited framing).
// For now it handles basic routing and stub methods; upgrade to structured IPC later.
type Server struct {
	addr     string
	logger   *logging.Logger
	ln       net.Listener
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
	handlers map[string]HandlerFunc
}

// Error represents a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Request represents a JSON-RPC request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

// Response represents a JSON-RPC response.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
	ID      any    `json:"id,omitempty"`
}

func New(addr string, logger *logging.Logger) *Server {
	return &Server{
		addr:     addr,
		logger:   logger,
		handlers: make(map[string]HandlerFunc),
	}
}

// Register sets a handler for a method name.
func (s *Server) Register(method string, h HandlerFunc) {
	s.handlers[method] = h
}

func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	s.ln = ln
	s.running = true
	s.mu.Unlock()

	s.logger.Info("rpc server listening", logging.String("addr", s.addr))

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				s.mu.Lock()
				active := s.running
				s.mu.Unlock()
				if !active {
					return
				}
				s.logger.Error("rpc accept error", logging.Error(err))
				continue
			}
			s.wg.Add(1)
			go s.handleConn(conn)
		}
	}()
	return nil
}

const (
	connReadTimeout  = 60 * time.Second
	connWriteTimeout = 30 * time.Second
)

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(connReadTimeout))
		var req Request
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			// stop on EOF/closed connection (normal disconnect)
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			// timeout is normal idle disconnect
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return
			}
			// log and stop (for simplicity, no partial recovery)
			s.logger.Error("rpc decode error", logging.Error(err))
			return
		}

		resp := s.dispatchSafe(req)
		_ = conn.SetWriteDeadline(time.Now().Add(connWriteTimeout))
		if err := enc.Encode(resp); err != nil {
			s.logger.Error("rpc encode error", logging.Error(err))
			return
		}
	}
}

func (s *Server) dispatchSafe(req Request) (resp Response) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("rpc handler panic", logging.Any("panic", r), logging.String("method", req.Method))
			resp = Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: -32603, Message: fmt.Sprintf("internal error: %v", r)},
				ID:      req.ID,
			}
		}
	}()
	return s.dispatch(req)
}

func (s *Server) dispatch(req Request) Response {
	if req.JSONRPC != "2.0" {
		return Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32600, Message: "invalid request: jsonrpc must be 2.0"},
			ID:      req.ID,
		}
	}

	handler, ok := s.handlers[req.Method]
	if !ok {
		return Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32601, Message: "method not found"},
			ID:      req.ID,
		}
	}

	result, rpcErr := handler(req.Params)
	return Response{
		JSONRPC: "2.0",
		Result:  result,
		Error:   rpcErr,
		ID:      req.ID,
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	_ = s.ln.Close()
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return fmt.Errorf("rpc shutdown timeout")
	}
}
