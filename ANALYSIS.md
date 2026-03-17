# SearchContext Token 验证流程完整分析

## 1️⃣ MCP 入口点 - cmd/mcp/main.go 中的 search_context 工具处理

**文件路径**: D:\code\acemcp-go\cmd\mcp\main.go (行 319-356)

### 处理流程 - search_context 工具调用

当 MCP 客户端调用 search_context 工具时，dispatch() 函数在 tools/call 方法中处理：

【从文件行 319-356 提取】

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

        // ⚠️ 关键调用点：调用 Daemon 的 SearchContext RPC 方法
        raw, callErr := callDaemon(daemonAddr, "SearchContext", map[string]any{
            "project_root_path": projectRoot, 
            "query": query,
        })
        if callErr != nil {
            // 如果 Daemon 返回任何错误（包括 401），立即返回错误给客户端
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

### callDaemon() 函数的实现 - 行 368-393

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
        // ⚠️ 关键点：检查 Daemon 返回的 RPC 错误
        if resp.Error != nil {
            return nil, fmt.Errorf("%s", resp.Error.Message)
        }
        return resp.Result, nil
    }

**关键点**：Daemon 的任何 RPC 错误都会直接转换为 error，立即返回给 MCP 客户端。
如果 Daemon 内部因为 token 失效而返回 RPC 错误，会在这里被拦截并返回。

---

## 2️⃣ Daemon RPC 处理层 - internal/rpc/handlers.go

**文件路径**: D:\code\acemcp-go\internal\rpc\handlers.go (行 35-45)

【完整代码】

    s.Register("SearchContext", func(params json.RawMessage) (any, *Error) {
        var p searchParams
        if err := json.Unmarshal(params, &p); err != nil || p.ProjectRootPath == "" || p.Query == "" {
            return nil, &Error{Code: -32602, Message: "invalid params: project_root_path and query required"}
        }
        // ⚠️ 直接调用 indexer 服务的 SearchContext 方法
        res, err := idx.SearchContext(p.ProjectRootPath, p.Query)
        if err != nil {
            // 任何错误（包括 HTTP 401）都会被转换为 RPC 错误返回给 MCP
            return nil, &Error{Code: -32002, Message: err.Error()}
        }
        return res, nil
    })

**错误处理**：
- IndexProject 返回错误 → RPC Error Code: -32001
- SearchContext 返回错误 → RPC Error Code: -32002

如果 indexer.SearchContext() 中 token 验证失败（HTTP 401），错误会以字符串形式包装在 -32002 错误中返回。

---

## 3️⃣ Indexer 实现层 - internal/indexer/indexer.go

### SearchContext 主方法 (行 1912-2211)

**关键部分 1: 初始化和准备 (1912-2046)**

    func (s *Service) SearchContext(projectRoot, query string) (*SearchResult, error) {
        startTime := time.Now()
        var indexMs int64
        if projectRoot == "" || query == "" {
            return nil, fmt.Errorf("project_root_path and query required")
        }

        normRoot := s.normalizePath(projectRoot)
        s.opLog.Infof(OpSearch, normRoot, "search started: %s", query)
        s.StartWatching(projectRoot)

        // 加载已索引的项目
        s.opMu.Lock()
        projects, err := s.loadProjects()
        s.opMu.Unlock()
        if err != nil {
            s.opLog.Errorf(OpSearch, normRoot, "load projects failed: %v", err)
            return nil, err
        }

        indexStart := time.Now()
        justIndexed := false
        hasPendingChanges := false

        // 检查子项目（如果存在则聚合搜索）
        indexedChildren := s.findChildProjects(projectRoot)
        
        if len(indexedChildren) > 0 {
            // ... 处理子项目索引
        } else if len(projects[normRoot]) == 0 {
            // 项目未索引，执行初始索引
            s.opLog.Info(OpSearch, normRoot, "project not indexed, starting initial index")
            if _, err := s.IndexProject(projectRoot); err != nil {
                s.opLog.Errorf(OpSearch, normRoot, "initial index failed: %v", err)
                return nil, err
            }
            justIndexed = true
        } else {
            // 项目已索引，检查 pending changes
            pendingChanges := s.flushPendingChanges(normRoot)
            hasPendingChanges = pendingChanges > 0
            go s.incrementalScan(projectRoot, normRoot)
        }

**关键部分 2: ⚠️ 等待远程索引就绪 (2083-2095)**

    if justIndexed {
        // First-time index: wait for all blobs
        ok := s.waitForBlobsIndexedOptimized(normRoot, blobNames, false)
        if !ok {
            s.opLog.Warnf(OpSearch, normRoot, "remote index not ready after initial wait, proceeding with search")
        }
    } else if hasPendingChanges {
        // Has pending changes: use optimized sampling for faster response
        ok := s.waitForBlobsIndexedOptimized(normRoot, blobNames, true)
        if !ok {
            s.opLog.Warnf(OpSearch, normRoot, "remote incremental index not ready after wait, proceeding with search")
        }
    }

**这里调用的 waitForBlobsIndexedOptimized 会调用 find-missing API，可能返回 401！**

**关键部分 3: ⚠️ 搜索 API 调用（带重试机制）(2097-2192)**

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

    // 搜索 API 调用 - 有 3 次重试机制
    var res struct {
        Formatted string json:"formatted_retrieval"
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
        ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
        req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
        if err != nil {
            cancel()
            return nil, err
        }
        // ⚠️ Token 在此传递（如果 token 无效，返回 401）
        req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
        req.Header.Set("Content-Type", "application/json")

        apiStart := time.Now()
        resp, err := s.client.Do(req)
        if err != nil {
            cancel()
            s.logger.Warn("search request failed, retrying",
                logging.Error(err),
                logging.Int("attempt", attempt+1),
            )
            if attempt < maxRetries-1 {
                delay := baseDelay * time.Duration(attempt+1)
                s.opLog.Warnf(OpSearch, normRoot, "search request failed, retrying in %v (attempt %d/%d)", 
                    delay, attempt+1, maxRetries)
                time.Sleep(delay)
            }
            continue
        }
        cancel()

        // ⚠️ 错误处理：任何非 2xx 状态码都被当作错误
        if resp.StatusCode < 200 || resp.StatusCode >= 300 {
            limited := io.LimitReader(resp.Body, 16*1024)
            b, _ := io.ReadAll(limited)
            reqID := resp.Header.Get("x-request-id")
            if reqID == "" {
                reqID = resp.Header.Get("x-amzn-requestid")
            }
            _ = resp.Body.Close()
            he := &apiHTTPError{Operation: "search", StatusCode: resp.StatusCode, Body: string(b), RequestID: reqID}
            s.opLog.Error(OpSearch, normRoot, fmt.Sprintf("search API error (HTTP %d)", resp.StatusCode), he.Error())
            if attempt < maxRetries-1 {
                delay := baseDelay * time.Duration(attempt+1)
                time.Sleep(delay)
                continue
            }
            // ⚠️ 重试 3 次都失败，立即返回 apiHTTPError（可能是 401）
            return nil, he
        }

        if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
            _ = resp.Body.Close()
            s.opLog.Errorf(OpSearch, normRoot, "decode response failed: %v", err)
            if attempt < maxRetries-1 {
                delay := baseDelay * time.Duration(attempt+1)
                time.Sleep(delay)
                continue
            }
            return nil, err
        }
        _ = resp.Body.Close()
        apiMs = time.Since(apiStart).Milliseconds()

        if res.Formatted == "" || len(res.Formatted) < 10 {
            s.opLog.Warnf(OpSearch, normRoot, "search returned empty/short result (len=%d), retrying (attempt %d/%d)", 
                len(res.Formatted), attempt+1, maxRetries)
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

    // ✅ 返回成功结果
    return &SearchResult{
        Status:  "success",
        Message: "search completed",
        Output:  res.Formatted,
    }, nil

---

## 4️⃣ ⚠️ Token 验证触发点 - find-missing API 调用

### waitForBlobsIndexedOptimized 方法 (行 973-1075)

【完整代码】

    func (s *Service) waitForBlobsIndexedOptimized(normRoot string, blobNames []string, isIncremental bool) bool {
        if len(blobNames) == 0 {
            return true
        }

        // 采样逻辑：对于大集合，采样 100 个 blob 进行检查
        sampleSize := 100
        if isIncremental && len(blobNames) > sampleSize {
            sampleSize = min(100, len(blobNames)/10+10)
        }

        step := len(blobNames) / sampleSize
        if step < 1 {
            step = 1
        }

        sampled := make([]string, 0, sampleSize)
        for i := 0; i < len(blobNames) && len(sampled) < sampleSize; i += step {
            sampled = append(sampled, blobNames[i])
        }

        maxWait := 30 * time.Second
        if !isIncremental {
            maxWait = 60 * time.Second
        }
        pollInterval := 2 * time.Second
        elapsed := time.Duration(0)
        lastNonindexed := -1
        stableCount := 0
        errCount := 0
        var lastErr error

        s.opLog.Infof(OpSearch, normRoot, "waiting for %d blobs (sampled from %d, incremental=%v)",
            len(sampled), len(blobNames), isIncremental)

        for elapsed < maxWait {
            time.Sleep(pollInterval)
            elapsed += pollInterval

            ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
            // ⚠️ 关键调用：find-missing API，可能返回 401
            res, err := s.findMissing(ctx, sampled)
            cancel()
            if err != nil {
                errCount++
                lastErr = err
                if errCount <= 2 || errCount%5 == 0 {
                    // 如果返回 401，错误消息会在这里记录
                    s.opLog.Warnf(OpSearch, normRoot, "find-missing failed: %v", err)
                }
                // ⚠️ 重要：继续轮询（不立即返回错误）
                continue
            }

            nonindexed := len(res.NonindexedBlobNames)
            unknown := len(res.UnknownBlobNames)

            if nonindexed == 0 && unknown == 0 {
                s.opLog.Infof(OpSearch, normRoot, "sampled blobs indexed after %v", elapsed)
                return true
            }

            // 对于增量更新，允许最多 5% 的失败
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

        // ⚠️ 超时处理：关键的行为差异
        if isIncremental {
            // 增量更新：忽略错误并继续
            s.opLog.Warnf(OpSearch, normRoot, "timeout waiting for incremental index after %v, proceeding anyway", maxWait)
            if lastErr != nil {
                s.opLog.Warnf(OpSearch, normRoot, "last find-missing error: %v", lastErr)
            }
            return true  // ← 继续搜索（即使 find-missing 返回 401）
        }

        // 首次索引：返回 false，搜索会继续但有警告
        s.opLog.Warnf(OpSearch, normRoot, "timeout waiting for blobs to be indexed after %v", maxWait)
        if lastErr != nil {
            s.opLog.Warnf(OpSearch, normRoot, "last find-missing error: %v", lastErr)
        }
        return false
    }

### 401 错误结构 (行 1077-1110)

    type apiHTTPError struct {
        Operation  string
        StatusCode int
        Body       string
        RequestID  string
    }

    func (e *apiHTTPError) Error() string {
        body := strings.TrimSpace(e.Body)
        if len(body) > 1024 {
            body = body[:1024]
        }
        op := strings.TrimSpace(e.Operation)
        if op == "" {
            op = "request"
        }
        if e.RequestID != "" {
            return fmt.Sprintf("%s failed (status %d, request_id %s): %s", op, e.StatusCode, e.RequestID, body)
        }
        return fmt.Sprintf("%s failed (status %d): %s", op, e.StatusCode, body)
    }

    func isTokenError(err error) bool {
        code, ok := statusCodeFromErr(err)
        return ok && code == http.StatusUnauthorized
    }

### find-missing 实现 (行 740-825)

【findMissingSingle - 直接调用 API 的部分】

    func (s *Service) findMissingSingle(ctx context.Context, blobNames []string) (*findMissingResult, error) {
        payload := map[string]any{
            "mem_object_names": blobNames,
        }
        body, _ := json.Marshal(payload)
        u, err := url.Parse(strings.TrimRight(s.cfg.BaseURL, "/") + "/find-missing")
        if err != nil {
            return nil, fmt.Errorf("invalid base_url: %w", err)
        }

        req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
        if err != nil {
            return nil, err
        }
        // ⚠️ Token 在此传递
        req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
        req.Header.Set("Content-Type", "application/json")

        resp, err := s.client.Do(req)
        if err != nil {
            return nil, err
        }

        reqID := resp.Header.Get("x-request-id")
        if reqID == "" {
            reqID = resp.Header.Get("x-amzn-requestid")
        }

        // ⚠️ 任何非 2xx 都返回错误（包括 401）
        if resp.StatusCode < 200 || resp.StatusCode >= 300 {
            limited := io.LimitReader(resp.Body, 16*1024)
            b, _ := io.ReadAll(limited)
            _ = resp.Body.Close()
            return nil, &apiHTTPError{
                Operation: "find-missing", 
                StatusCode: resp.StatusCode,  // 401
                Body: string(b), 
                RequestID: reqID,
            }
        }

        var res findMissingResult
        if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
            _ = resp.Body.Close()
            return nil, err
        }
        _ = resp.Body.Close()
        return &res, nil
    }

---

## 🔍 完整错误传播流程

### 场景 1：首次索引时 find-missing 返回 401

    SearchContext(projectRoot, query)
        ↓
    waitForBlobsIndexedOptimized(normRoot, blobNames, false)  [isIncremental=false]
        ↓
    [轮询循环，每 2 秒调用一次]
    
    findMissing(ctx, sampled)
        ↓
    findMissingSingle(ctx, blobNames)
        ↓ [HTTP 401 Unauthorized]
    
    return nil, &apiHTTPError{
        Operation: "find-missing", 
        StatusCode: 401,
        Body: "..."
        RequestID: "..."
    }
        ↓
    [在 waitForBlobsIndexedOptimized 中捕获]
    if err != nil {
        errCount++
        lastErr = err
        s.opLog.Warnf(..., "find-missing failed: %v", err)  // ← 记录 401 错误
        continue  // ← 继续轮询
    }
        ↓
    [轮询 30 秒后超时]
    
    if isIncremental {  // false，所以走下面的分支
        ...
    }
    
    s.opLog.Warnf(..., "timeout waiting for blobs to be indexed after 30s", maxWait)
    if lastErr != nil {
        s.opLog.Warnf(..., "last find-missing error: find-missing failed (status 401): ...")
    }
    return false  // ← 返回 false
        ↓
    [在 SearchContext 中，line 2086]
    if !ok {
        s.opLog.Warnf(..., "remote index not ready after initial wait, proceeding with search")
    }
        ↓
    [继续执行，调用搜索 API...]
    
    Search API with 3 retries
        ↓
    req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
        ↓ [Token 失效，返回 401]
    
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        he := &apiHTTPError{Operation: "search", StatusCode: 401, ...}
        s.opLog.Error(..., "search API error (HTTP 401)", ...)
        if attempt < maxRetries-1 {
            time.Sleep(delay)
            continue  // 重试
        }
        return nil, he  // ← 返回 401 错误
    }
        ↓
    [在 RPC handlers.go 中]
    res, err := idx.SearchContext(...)
    if err != nil {
        return nil, &Error{Code: -32002, Message: err.Error()}
    }
        ↓
    [在 MCP 的 callDaemon 中]
    if resp.Error != nil {
        return nil, fmt.Errorf("%s", resp.Error.Message)
    }
        ↓
    [在 MCP 的 tools/call 中]
    if callErr != nil {
        resp.Result = map[string]any{
            "content": []map[string]any{{"type": "text", "text": callErr.Error()}},
            "isError": true,
        }
        return resp
    }
        ↓
    【最终返回给客户端】
    {
        "content": [{
            "type": "text",
            "text": "search failed (status 401): {...error details...}"
        }],
        "isError": true
    }

### 场景 2：增量更新时 find-missing 返回 401

    SearchContext(projectRoot, query)
        ↓
    hasPendingChanges = true  [有待处理的文件变化]
        ↓
    waitForBlobsIndexedOptimized(normRoot, blobNames, true)  [isIncremental=true]
        ↓
    [轮询，每次 find-missing 都返回 401]
    
    [30 秒超时后]
    
    if isIncremental {  // true！
        s.opLog.Warnf(..., "timeout waiting for incremental index after 30s, proceeding anyway")
        if lastErr != nil {
            s.opLog.Warnf(..., "last find-missing error: find-missing failed (status 401): ...")  // 记录 401 但不中断
        }
        return true  // ← 直接返回 true，忽略 401 错误！
    }
        ↓
    [在 SearchContext 中，line 2092]
    if !ok {
        // 这里 ok=true，所以不会执行
    }
        ↓
    [继续执行搜索 API...]
        ↓
    Search API 同样因 token 失效返回 401
        ↓
    【最终返回给客户端相同的 401 错误】

---

## 📊 总结表

| 组件 | 文件 | 行号 | 功能 | 错误处理 |
|-----|------|------|------|---------|
| MCP 工具处理 | cmd/mcp/main.go | 319-356 | 接收 search_context 调用，调用 Daemon | 立即返回错误给客户端 |
| callDaemon | cmd/mcp/main.go | 368-393 | 与 Daemon RPC 通信 | RPC 错误立即返回 |
| RPC 处理 | internal/rpc/handlers.go | 35-45 | 注册 SearchContext 方法 | 转换为 -32002 RPC 错误 |
| SearchContext | internal/indexer/indexer.go | 1912-2211 | 主搜索流程 | 调用 API 返回 401 时立即返回 |
| waitForBlobs | internal/indexer/indexer.go | 973-1075 | 等待远程索引就绪 | 首次=返回 false，增量=忽略错误返回 true |
| findMissing | internal/indexer/indexer.go | 740-825 | 调用 find-missing API | 返回 401 时构建 apiHTTPError |

---

## ✅ 关键发现

1. **find-missing 返回 401 时**：
   - 【首次索引】被记录为警告，等待 30 秒超时后返回 false，搜索继续
   - 【增量更新】被记录为警告，但直接返回 true，搜索立即继续
   - **在两种情况下都不会中断执行**

2. **搜索 API 返回 401 时**：
   - 重试 3 次（延迟分别为 2s, 4s, 6s）
   - 最后失败则返回 apiHTTPError{StatusCode: 401}

3. **错误传播路径**：
   `
   apiHTTPError{StatusCode: 401, Operation: "search", ...}
     ↓
   RPC Error{Code: -32002, Message: "search failed (status 401): ..."}
     ↓
   MCP Result{isError: true, content: [{type: "text", text: "search failed (status 401): ..."}]}
     ↓
   客户端收到的错误信息
   `

4. **Token 验证操作在两处触发**：
   - find-missing API（用于等待索引就绪）
   - search/codebase-retrieval API（实际搜索）

5. **当 find-missing 失败时，不会立即返回错误**，而是：
   - 继续轮询（如果还有时间）
   - 或跳过等待（增量更新）
   - 最终在搜索 API 也失效时才返回 401 错误
