# AGENTS.md - acemcp-go

## Commands
- **Build**: `make build` or `go build -o dist/acemcp-go-daemon ./cmd/daemon`
- **Test all**: `go test ./...`
- **Test single**: `go test -run TestName ./internal/package/...`
- **Deps**: `go mod tidy && go mod download`
- **Lint**: `go vet ./...`

## Architecture
- **cmd/daemon**: Main daemon process - JSON-RPC + HTTP management server
- **cmd/mcp**: MCP protocol proxy for IDE integration
- **internal/config**: Configuration via viper (TOML: ~/.acemcp/settings.toml)
- **internal/indexer**: Code indexing and semantic search
- **internal/rpc**: JSON-RPC service layer
- **internal/server**: HTTP management endpoints (/health, /status, /metrics)
- **internal/logging**: Structured logging with zap

## Code Style
- Go 1.22+, use `fmt.Errorf("context: %w", err)` for error wrapping
- Imports: stdlib first, then external, then internal (grouped with blank lines)
- Logging: use `logging.String()`, `logging.Error()` helpers with zap
- Config keys: UPPER_SNAKE_CASE in TOML, PascalCase in Go structs
- No comments unless complex logic; defer cleanup with `defer x.Close()`
