package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/agenthof/hub/internal/agentkit"
)

const planted = "Every entitlement is granted to a named agent, never to a whole team."

// upsert is one recorded memory_upsert call.
type upsert struct {
	Collection string
	ID         string
	Text       string
	Metadata   map[string]any
}

// stand is a stand-in for the per-run proxy: /v1/embeddings plus the MCP tool
// door (initialize -> session id -> tools/call) serving canned Drive results
// and recording every memory_upsert.
type stand struct {
	t     *testing.T
	notes map[string]string // docId -> body
	order []string          // docIds returned by search, in order

	mu      sync.Mutex
	queries []string
	upserts []upsert
}

func (s *stand) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.URL.Path == "/v1/embeddings" {
		var b struct{ Model, Input string }
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Model != "embed" || b.Input == "" {
			s.t.Errorf("embed body = %+v", b)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,1.5]}]}`))
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
	txt := func(x string) map[string]any {
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": x}}}
	}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess")
		reply(map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "stub", "version": "1"}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/call":
		name, _ := req.Params["name"].(string)
		args, _ := req.Params["arguments"].(map[string]any)
		s.mu.Lock()
		defer s.mu.Unlock()
		switch name {
		case "search":
			s.queries = append(s.queries, fmt.Sprint(args["query"]))
			var sb strings.Builder
			for _, id := range s.order {
				fmt.Fprintf(&sb, "%s Note %s sync\n", id, id)
			}
			reply(txt(sb.String()))
		case "readGoogleDoc":
			body, ok := s.notes[fmt.Sprint(args["documentId"])]
			if !ok {
				r := txt("not found")
				r["isError"] = true
				reply(r)
				return
			}
			reply(txt(body))
		case "memory_upsert":
			md, _ := args["metadata"].(map[string]any)
			s.upserts = append(s.upserts, upsert{
				Collection: fmt.Sprint(args["collection"]), ID: fmt.Sprint(args["id"]),
				Text: fmt.Sprint(args["text"]), Metadata: md,
			})
			reply(map[string]any{"content": []any{}, "structuredContent": map[string]any{"id": args["id"]}})
		default:
			s.t.Errorf("unexpected tool %q", name)
		}
	}
}

func filler(from, to int) string {
	var sb strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&sb, "Filler sentence number %d is here. ", i)
	}
	return sb.String()
}

func newStand(t *testing.T) (*stand, *httptest.Server) {
	s := &stand{
		t: t,
		notes: map[string]string{
			"n1": "Meeting notes - 2026-09-14\nAttendees: dana@example.com\n\nDecision one. " + planted + " Decision three.\n\nAction items follow.",
			"n2": "Weekly sync\n\nNo header here.",
			"n3": "Meeting notes - 2026-13-45\n\nGarbled date.",
			// One oversize paragraph with the planted sentence mid-way.
			"n4": "Meeting notes - 2026-10-01\n\n" + filler(0, 150) + planted + " " + filler(150, 400),
		},
		order: []string{"n1", "n2", "n3", "n4"},
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func run(t *testing.T, srv *httptest.Server) agentkit.Result {
	return ingest(context.Background(), srv.Client(), srv.URL+"/", "tok", agentkit.Request{})
}

func TestIngestIdsMetadataAndCounts(t *testing.T) {
	s, srv := newStand(t)
	res := run(t, srv)
	if !res.Success {
		t.Fatalf("failed: %s", res.Reason)
	}
	if !reflect.DeepEqual(s.queries, []string{"sync"}) {
		t.Errorf("queries = %v", s.queries)
	}
	byID := map[string]upsert{}
	for _, u := range s.upserts {
		if u.Collection != "notes" {
			t.Errorf("collection = %q", u.Collection)
		}
		byID[u.ID] = u
	}
	// n1: 3 paragraphs -> but header+attendees is one, decisions one, actions one.
	for _, id := range []string{"n1#0", "n1#1", "n1#2", "n2#0", "n2#1", "n3#0", "n3#1", "n4#0"} {
		if _, ok := byID[id]; !ok {
			t.Errorf("missing id %s (have %v)", id, ids(s.upserts))
		}
	}
	if m := byID["n1#1"].Metadata; m["source"] != "n1" || m["chunk_index"] != float64(1) || m["date"] != "2026-09-14" {
		t.Errorf("n1#1 metadata = %v", m)
	}
	if m := byID["n2#0"].Metadata; m["date"] != "" || m["source"] != "n2" {
		t.Errorf("undated metadata = %v", m)
	}
	if m := byID["n3#0"].Metadata; m["date"] != "" {
		t.Errorf("garbled-date metadata = %v", m)
	}
	undated := 4 // n2 (2 chunks) + n3 (2 chunks)
	want := fmt.Sprintf("indexed %d chunks (%d undated)", len(s.upserts), undated)
	if res.Artifact != want {
		t.Errorf("artifact = %q, want %q", res.Artifact, want)
	}
	for _, u := range s.upserts {
		if len(u.Text) > maxChunk || strings.TrimSpace(u.Text) == "" {
			t.Errorf("%s: bad chunk size %d", u.ID, len(u.Text))
		}
	}
}

func TestIngestPlantedSentenceSurvivesIntact(t *testing.T) {
	s, srv := newStand(t)
	if res := run(t, srv); !res.Success {
		t.Fatal(res.Reason)
	}
	for _, src := range []string{"n1", "n4"} {
		n := 0
		for _, u := range s.upserts {
			if u.Metadata["source"] == src && strings.Contains(u.Text, planted) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: planted sentence in %d chunks, want 1", src, n)
		}
	}
	// n4 really was split (oversize paragraph).
	c := 0
	for _, u := range s.upserts {
		if u.Metadata["source"] == "n4" {
			c++
		}
	}
	if c < 2 {
		t.Errorf("n4 produced %d chunks, want >= 2", c)
	}
}

func TestIngestIdempotentIDs(t *testing.T) {
	s, srv := newStand(t)
	run(t, srv)
	first := ids(s.upserts)
	n := len(s.upserts)
	run(t, srv)
	second := ids(s.upserts[n:])
	if !reflect.DeepEqual(first, second) {
		t.Errorf("ids differ between runs:\n%v\n%v", first, second)
	}
}

func TestIngestFailsClearly(t *testing.T) {
	s, srv := newStand(t)
	s.order = append(s.order, "ghost") // search hit with no readable doc
	res := run(t, srv)
	if res.Success || !strings.Contains(res.Reason, "ghost") {
		t.Errorf("res = %+v", res)
	}
}

func TestChunkNeverSplitsSentence(t *testing.T) {
	p := "Alpha one. Beta two is longer. Gamma three. Delta four."
	got := chunk(p, 30)
	for _, c := range got {
		if len(c) > 30 {
			t.Errorf("chunk too long: %q", c)
		}
		if !strings.HasSuffix(c, ".") {
			t.Errorf("chunk ends mid-sentence: %q", c)
		}
	}
	if strings.Join(got, " ") != p {
		t.Errorf("content lost: %q", got)
	}
}

func ids(us []upsert) []string {
	var out []string
	for _, u := range us {
		out = append(out, u.ID)
	}
	sort.Strings(out)
	return out
}
