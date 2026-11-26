package agentkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

const (
	mcpProtocolVersion = "2025-06-18"
	headerSessionID    = "Mcp-Session-Id"
	headerProtocolVer  = "Mcp-Protocol-Version"
)

// Wire tool names, fixed by the gateway's resource configuration.
const (
	toolMemoryUpsert = "memory_upsert"
	toolMemorySearch = "memory_search"
	toolDriveSearch  = "search"
	toolDriveRead    = "readGoogleDoc"
)

// MemoryHit is one memory_search result, carrying the stored chunk text and
// metadata.
type MemoryHit struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata"`
}

// DriveHit is one drive search result: a document id and its title.
type DriveHit struct {
	ID    string
	Title string
}

// ToolClient is a minimal MCP Streamable-HTTP client for the per-run proxy's
// tool door. It performs the MCP handshake lazily on first use and reuses the
// session afterwards. It is safe for concurrent use.
type ToolClient struct {
	c        *http.Client
	endpoint string
	token    string

	mu      sync.Mutex
	ready   bool
	session string
	version string
	nextID  int64
}

// NewToolClient returns a client for the tool door behind proxyURL (http(s) or
// unix://), authenticating with runToken. Redirects are never followed.
func NewToolClient(client *http.Client, proxyURL, runToken string) *ToolClient {
	c, base := clientAndBase(client, proxyURL)
	return &ToolClient{c: c, endpoint: base + "/", token: runToken}
}

// MemoryUpsert stores text with its vector and metadata and returns the id.
// A non-empty id is a stable caller id: upserting the same id overwrites. An
// empty id is omitted and the store assigns one.
func (t *ToolClient) MemoryUpsert(ctx context.Context, collection, id, text string, vector []float32, metadata map[string]any) (string, error) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	args := map[string]any{"collection": collection, "text": text, "vector": vector, "metadata": metadata}
	if id != "" {
		args["id"] = id
	}
	var out struct {
		ID string `json:"id"`
	}
	err := t.callTool(ctx, toolMemoryUpsert, args, &out, nil)
	return out.ID, err
}

// MemorySearch returns the k nearest entries to vector.
func (t *ToolClient) MemorySearch(ctx context.Context, collection string, vector []float32, k int) ([]MemoryHit, error) {
	var out struct {
		Results []MemoryHit `json:"results"`
	}
	err := t.callTool(ctx, toolMemorySearch, map[string]any{
		"collection": collection, "vector": vector, "k": k,
	}, &out, nil)
	return out.Results, err
}

// DriveSearch searches the drive. Each result line is "<docId> <title>".
func (t *ToolClient) DriveSearch(ctx context.Context, query string) ([]DriveHit, error) {
	var text string
	if err := t.callTool(ctx, toolDriveSearch, map[string]any{"query": query}, nil, &text); err != nil {
		return nil, err
	}
	var hits []DriveHit
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, title, _ := strings.Cut(line, " ")
		hits = append(hits, DriveHit{ID: id, Title: strings.TrimSpace(title)})
	}
	return hits, nil
}

// DriveRead returns the body of the document with the given id.
func (t *ToolClient) DriveRead(ctx context.Context, docID string) (string, error) {
	var text string
	err := t.callTool(ctx, toolDriveRead, map[string]any{"documentId": docID}, nil, &text)
	return text, err
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *rpcError        `json:"error,omitempty"`
}

// callTool runs tools/call. A non-nil structured receives the result's
// structuredContent; a non-nil text receives the concatenated text content.
func (t *ToolClient) callTool(ctx context.Context, name string, args map[string]any, structured any, text *string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.initLocked(ctx); err != nil {
		return err
	}
	raw, err := t.requestLocked(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return fmt.Errorf("mcp %s: %w", name, err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("mcp %s: decode result: %w", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	if res.IsError {
		return fmt.Errorf("mcp %s: tool error: %s", name, sb.String())
	}
	if structured != nil {
		if len(res.StructuredContent) == 0 {
			return fmt.Errorf("mcp %s: no structuredContent in result", name)
		}
		if err := json.Unmarshal(res.StructuredContent, structured); err != nil {
			return fmt.Errorf("mcp %s: decode structuredContent: %w", name, err)
		}
	}
	if text != nil {
		*text = sb.String()
	}
	return nil
}

// initLocked performs initialize + notifications/initialized once.
func (t *ToolClient) initLocked(ctx context.Context) error {
	if t.ready {
		return nil
	}
	raw, err := t.requestLocked(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agenthof-agentkit", "version": "1"},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &ir); err != nil {
		return fmt.Errorf("mcp initialize: decode: %w", err)
	}
	t.version = ir.ProtocolVersion
	if t.version == "" {
		t.version = mcpProtocolVersion
	}
	if err := t.notifyLocked(ctx, "notifications/initialized"); err != nil {
		return fmt.Errorf("mcp initialized: %w", err)
	}
	t.ready = true
	return nil
}

func (t *ToolClient) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if t.session != "" {
		req.Header.Set(headerSessionID, t.session)
	}
	if t.version != "" {
		req.Header.Set(headerProtocolVer, t.version)
	}
	return req, nil
}

func (t *ToolClient) notifyLocked(ctx context.Context, method string) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	req, err := t.newRequest(ctx, body)
	if err != nil {
		return err
	}
	resp, err := t.c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// requestLocked sends one JSON-RPC request and returns its result.
func (t *ToolClient) requestLocked(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.nextID++
	id := t.nextID
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := t.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := t.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	if sid := resp.Header.Get(headerSessionID); sid != "" {
		t.session = sid
	}
	ct := resp.Header.Get("Content-Type")
	var msg *rpcMessage
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		msg, err = readSSEResult(io.LimitReader(resp.Body, maxResponseBytes), id)
	case strings.HasPrefix(ct, "application/json"):
		msg, err = readJSONResult(io.LimitReader(resp.Body, maxResponseBytes), id)
	default:
		err = fmt.Errorf("unexpected content type %q", ct)
	}
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", msg.Error.Code, msg.Error.Message)
	}
	return msg.Result, nil
}

func isResponseFor(m *rpcMessage, id int64) bool {
	if m.ID == nil || m.Method != "" {
		return false
	}
	var got int64
	return json.Unmarshal(*m.ID, &got) == nil && got == id
}

func readJSONResult(r io.Reader, id int64) (*rpcMessage, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var m rpcMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !isResponseFor(&m, id) {
		return nil, errors.New("response does not match request id")
	}
	return &m, nil
}

// readSSEResult scans an event stream for the JSON-RPC response to id,
// skipping any server notifications or requests interleaved before it.
func readSSEResult(r io.Reader, id int64) (*rpcMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxResponseBytes)
	var data []string
	flush := func() *rpcMessage {
		if len(data) == 0 {
			return nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		var m rpcMessage
		if json.Unmarshal([]byte(payload), &m) != nil || !isResponseFor(&m, id) {
			return nil
		}
		return &m
	}
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if line == "" {
			if m := flush(); m != nil {
				return m, nil
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if m := flush(); m != nil {
		return m, nil
	}
	return nil, errors.New("event stream ended without a response")
}
