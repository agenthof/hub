package agentkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// mcpStub is a realistic MCP Streamable-HTTP server: it rejects any call made
// before initialize, issues a session id on initialize and requires it on every
// later request, requires the bearer, and returns tool results as
// structuredContent (or text content for the drive tools).
type mcpStub struct {
	t   *testing.T
	sse bool
	// noID expects memory_upsert without the optional id argument.
	noID bool

	mu          sync.Mutex
	initialized bool
	notified    bool
	inits       int
	calls       []string
	lastArgs    map[string]any
}

const stubSession = "sess-123"

func (s *mcpStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Method != http.MethodPost {
		s.t.Errorf("got %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if a := r.Header.Get("Accept"); !strings.Contains(a, "application/json") || !strings.Contains(a, "text/event-stream") {
		http.Error(w, "bad accept", http.StatusNotAcceptable)
		return
	}
	var req struct {
		ID     *int64         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Method == "initialize" {
		s.inits++
		s.initialized = true
		w.Header().Set(headerSessionID, stubSession)
		s.reply(w, *req.ID, map[string]any{"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "stub", "version": "1"}})
		return
	}
	if !s.initialized {
		http.Error(w, "not initialized", http.StatusBadRequest)
		return
	}
	if r.Header.Get(headerSessionID) != stubSession {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}
	switch req.Method {
	case "notifications/initialized":
		s.notified = true
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		if !s.notified {
			http.Error(w, "initialized notification missing", http.StatusBadRequest)
			return
		}
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)
		s.calls = append(s.calls, name)
		s.lastArgs = args
		s.reply(w, *req.ID, s.tool(name, args))
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
	}
}

func text(s string) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}
}

// wantArgs asserts the tool received exactly the expected argument names, so a
// client that drops or renames an argument fails.
func (s *mcpStub) wantArgs(name string, args map[string]any, keys ...string) {
	if len(args) != len(keys) {
		s.t.Errorf("%s: args = %v, want keys %v", name, args, keys)
	}
	for _, k := range keys {
		if _, ok := args[k]; !ok {
			s.t.Errorf("%s: missing arg %q in %v", name, k, args)
		}
	}
}

func (s *mcpStub) tool(name string, args map[string]any) map[string]any {
	switch name {
	case "memory_upsert":
		if s.noID {
			s.wantArgs(name, args, "collection", "text", "vector", "metadata")
		} else {
			s.wantArgs(name, args, "collection", "id", "text", "vector", "metadata")
		}
		return map[string]any{"content": []any{}, "structuredContent": map[string]any{"id": "doc1#0"}}
	case "memory_search":
		s.wantArgs(name, args, "collection", "vector", "k")
		return map[string]any{"content": []any{}, "structuredContent": map[string]any{
			"results": []any{
				map[string]any{"id": "doc1#0", "score": 0.9, "text": "chunk one", "metadata": map[string]any{"source": "d1", "date": "2026-09-14"}},
				map[string]any{"id": "doc2#1", "score": 0.5, "text": "chunk two", "metadata": map[string]any{}}}}}
	case "search":
		s.wantArgs(name, args, "query")
		return text("d1 Entitlements sync\nd2 Token handling sync\n\n")
	case "readGoogleDoc":
		s.wantArgs(name, args, "documentId")
		if args["documentId"] == "missing" {
			r := text("not found")
			r["isError"] = true
			return r
		}
		return text("Meeting notes - 2026-09-14\nbody")
	}
	return text("unknown tool")
}

func (s *mcpStub) reply(w http.ResponseWriter, id int64, result any) {
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if s.sse {
		w.Header().Set("Content-Type", "text/event-stream")
		// A server notification precedes the real response.
		_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/message\",\"params\":{}}\n\n")
		_, _ = fmt.Fprintf(w, "event: message\r\ndata: %s\r\n\r\n", msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(msg)
}

func exercise(t *testing.T, tc *ToolClient, stub *mcpStub) {
	t.Helper()
	ctx := context.Background()
	id, err := tc.MemoryUpsert(ctx, "meetings", "d1#0", "hello", []float32{1, 2}, map[string]any{"source": "d1"})
	if err != nil || id != "doc1#0" {
		t.Fatalf("upsert = %q, %v", id, err)
	}
	if stub.lastArgs["collection"] != "meetings" || stub.lastArgs["id"] != "d1#0" || stub.lastArgs["text"] != "hello" {
		t.Errorf("upsert args = %v", stub.lastArgs)
	}
	hits, err := tc.MemorySearch(ctx, "meetings", []float32{1, 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hits, []MemoryHit{
		{ID: "doc1#0", Score: 0.9, Text: "chunk one", Metadata: map[string]any{"source": "d1", "date": "2026-09-14"}},
		{ID: "doc2#1", Score: 0.5, Text: "chunk two", Metadata: map[string]any{}},
	}) {
		t.Fatalf("hits = %v", hits)
	}
	if stub.lastArgs["k"] != float64(2) {
		t.Errorf("search args = %v", stub.lastArgs)
	}
	dh, err := tc.DriveSearch(ctx, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dh, []DriveHit{{"d1", "Entitlements sync"}, {"d2", "Token handling sync"}}) {
		t.Fatalf("drive hits = %v", dh)
	}
	body, err := tc.DriveRead(ctx, "d1")
	if err != nil || !strings.HasPrefix(body, "Meeting notes - 2026-09-14") {
		t.Fatalf("read = %q, %v", body, err)
	}
	if stub.lastArgs["documentId"] != "d1" {
		t.Errorf("read args = %v", stub.lastArgs)
	}
	if _, err := tc.DriveRead(ctx, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want tool error, got %v", err)
	}
	if want := []string{"memory_upsert", "memory_search", "search", "readGoogleDoc", "readGoogleDoc"}; !reflect.DeepEqual(stub.calls, want) {
		t.Fatalf("calls = %v", stub.calls)
	}
	if stub.inits != 1 {
		t.Fatalf("initialize ran %d times, want 1 (session reused)", stub.inits)
	}
}

func TestToolClientJSONHandshakeAndTools(t *testing.T) {
	stub := &mcpStub{t: t}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	exercise(t, NewToolClient(srv.Client(), srv.URL+"/", "tok"), stub)
}

func TestToolClientSSEHandshakeAndTools(t *testing.T) {
	stub := &mcpStub{t: t, sse: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	exercise(t, NewToolClient(srv.Client(), srv.URL, "tok"), stub)
}

func TestToolClientOverUnixSocket(t *testing.T) {
	for name, sse := range map[string]bool{"json": false, "sse": true} {
		t.Run(name, func(t *testing.T) {
			stub := &mcpStub{t: t, sse: sse}
			u := serveUnix(t, stub)
			exercise(t, NewToolClient(nil, u, "tok"), stub)
		})
	}
}

func TestToolClientBadBearerErrors(t *testing.T) {
	stub := &mcpStub{t: t}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	tc := NewToolClient(srv.Client(), srv.URL, "wrong")
	if _, err := tc.DriveRead(context.Background(), "d1"); err == nil {
		t.Fatal("want error on 401")
	}
}

func TestToolClientRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"bad args"}}`, *req.ID)
		}
	}))
	defer srv.Close()
	tc := NewToolClient(srv.Client(), srv.URL, "tok")
	if _, err := tc.MemorySearch(context.Background(), "c", []float32{1}, 1); err == nil || !strings.Contains(err.Error(), "bad args") {
		t.Fatalf("want rpc error, got %v", err)
	}
}

func TestToolClientMissingStructuredContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"ok"}]}}`, *req.ID)
		}
	}))
	defer srv.Close()
	tc := NewToolClient(srv.Client(), srv.URL, "tok")
	if _, err := tc.MemorySearch(context.Background(), "c", []float32{1}, 1); err == nil || !strings.Contains(err.Error(), "structuredContent") {
		t.Fatalf("want missing structuredContent error, got %v", err)
	}
}

func TestMemoryUpsertOmitsEmptyID(t *testing.T) {
	stub := &mcpStub{t: t, noID: true}
	srv := httptest.NewServer(stub)
	defer srv.Close()
	tc := NewToolClient(srv.Client(), srv.URL, "tok")
	if _, err := tc.MemoryUpsert(context.Background(), "c", "", "x", []float32{1}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := stub.lastArgs["id"]; ok {
		t.Fatalf("empty id was sent: %v", stub.lastArgs)
	}
}
