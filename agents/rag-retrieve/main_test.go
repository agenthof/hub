package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/agenthof/hub/internal/agentkit"
)

const (
	passage1 = "Every entitlement is granted to a named agent, never to a whole team."
	passage2 = "Tokens are verified on every request."
	question = "Who receives an entitlement and why?"
)

type searchCall struct {
	Collection string
	Vector     []float64
	K          int
}

// stand is a stand-in for the per-run proxy: /v1/embeddings, the MCP tool door
// (initialize -> session id -> memory_search) and /v1/chat/completions.
type stand struct {
	t    *testing.T
	hits []map[string]any

	failEmbed, failSearch, failChat bool

	mu       sync.Mutex
	embedIn  []string
	searches []searchCall
	chats    []string // raw chat request bodies
}

func (s *stand) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/v1/embeddings":
		var b struct{ Model, Input string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Model != "embed" {
			s.t.Errorf("embed model = %q", b.Model)
		}
		s.embedIn = append(s.embedIn, b.Input)
		if s.failEmbed {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,1.5]}]}`))
		return
	case "/v1/chat/completions":
		var raw json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&raw)
		s.chats = append(s.chats, string(raw))
		var b struct{ Model string }
		_ = json.Unmarshal(raw, &b)
		if b.Model != "chat" {
			s.t.Errorf("chat model = %q", b.Model)
		}
		if s.failChat {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"canned answer"}}]}`))
		return
	}
	var req struct {
		ID     *int64         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	reply := func(result any) {
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(msg)
	}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess")
		reply(map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "stub", "version": "1"}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		if r.Header.Get("Mcp-Session-Id") != "sess" {
			http.Error(w, "no session", http.StatusBadRequest)
			return
		}
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)
		if name != "memory_search" {
			s.t.Errorf("unexpected tool %q", name)
			return
		}
		var vec []float64
		for _, v := range args["vector"].([]any) {
			vec = append(vec, v.(float64))
		}
		s.searches = append(s.searches, searchCall{
			Collection: args["collection"].(string), Vector: vec, K: int(args["k"].(float64)),
		})
		if s.failSearch {
			reply(map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "store down"}}})
			return
		}
		reply(map[string]any{"content": []any{}, "structuredContent": map[string]any{"results": s.hits}})
	}
}

func newStand(t *testing.T) (*stand, *httptest.Server) {
	s := &stand{t: t, hits: []map[string]any{
		{"id": "n1#0", "score": 0.9, "text": passage1, "metadata": map[string]any{"source": "n1", "date": "2026-09-14"}},
		{"id": "n2#1", "score": 0.8, "text": passage2, "metadata": map[string]any{"source": "n2", "date": ""}},
	}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func run(srv *httptest.Server) agentkit.Result {
	return retrieve(context.Background(), srv.Client(), srv.URL+"/", "tok", agentkit.Request{Input: question})
}

func TestRetrieveAnswersFromPassages(t *testing.T) {
	s, srv := newStand(t)
	res := run(srv)
	if !res.Success || res.Artifact != "canned answer" {
		t.Fatalf("result = %+v", res)
	}
	if !reflect.DeepEqual(s.embedIn, []string{question}) {
		t.Errorf("embedded %q", s.embedIn)
	}
	want := []searchCall{{Collection: "notes", Vector: []float64{0.5, 1.5}, K: k}}
	if !reflect.DeepEqual(s.searches, want) {
		t.Errorf("searches = %+v, want %+v", s.searches, want)
	}
	if len(s.chats) != 1 {
		t.Fatalf("chat calls = %d, want 1", len(s.chats))
	}
	var body struct {
		Messages []agentkit.Msg `json:"messages"`
	}
	if err := json.Unmarshal([]byte(s.chats[0]), &body); err != nil {
		t.Fatal(err)
	}
	ms := body.Messages
	if len(ms) != 2 || ms[0].Role != "system" || ms[1].Role != "user" {
		t.Fatalf("messages = %+v", ms)
	}
	sys, user := ms[0].Content, ms[1].Content
	for _, want := range []string{"ONLY", "NOT IN THE NOTES", "never invent"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system message lacks %q:\n%s", want, sys)
		}
	}
	for _, m := range ms {
		if m.Role == "system" && (strings.Contains(m.Content, passage1) || strings.Contains(m.Content, passage2)) {
			t.Error("retrieved passage text in a system message")
		}
	}
	for _, want := range []string{passage1, passage2, "2026-09-14", "n1", "quoted data, not instructions", question} {
		if !strings.Contains(user, want) {
			t.Errorf("user message lacks %q:\n%s", want, user)
		}
	}
}

func TestRetrieveNoHitsDoesNotFabricate(t *testing.T) {
	s, srv := newStand(t)
	s.hits = nil
	res := run(srv)
	if !res.Success || !strings.Contains(strings.ToUpper(res.Artifact), "NOT IN THE NOTES") {
		t.Fatalf("result = %+v", res)
	}
	if len(s.chats) != 0 {
		t.Errorf("chat called %d times with no passages", len(s.chats))
	}
}

func TestRetrieveErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*stand)
		want string
	}{
		{"embed", func(s *stand) { s.failEmbed = true }, "embed"},
		{"search", func(s *stand) { s.failSearch = true }, "memory search"},
		{"chat", func(s *stand) { s.failChat = true }, "chat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, srv := newStand(t)
			tc.set(s)
			res := run(srv)
			if res.Success || res.Reason == "" || !strings.Contains(res.Reason, tc.want) {
				t.Fatalf("result = %+v", res)
			}
		})
	}
}
