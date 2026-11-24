package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agenthof/hub/internal/agentkit"
)

const testInput = `{"menu":[` +
	`{"role":"billing","workflow":"answer","description":"answer billing questions"},` +
	`{"role":"refund","workflow":"find","description":"process a refund request"}],` +
	`"utterance":"I want my money back"}`

// stubProxy answers every chat call with reply and records what it saw.
func stubProxy(t *testing.T, reply string, gotModel *string, gotPrompt *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string         `json:"model"`
			Messages []agentkit.Msg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if gotModel != nil {
			*gotModel = body.Model
		}
		if gotPrompt != nil {
			var sb strings.Builder
			for _, m := range body.Messages {
				sb.WriteString(m.Content + "\n")
			}
			*gotPrompt = sb.String()
		}
		out, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
			"message": map[string]string{"role": "assistant", "content": reply}}}})
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRouterPicksMenuEntry(t *testing.T) {
	var gotModel, gotPrompt string
	srv := stubProxy(t, "2", &gotModel, &gotPrompt)

	got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: testInput})
	if !got.Success {
		t.Fatalf("expected success, got %+v", got)
	}
	want := `{"role":"refund","workflow":"find"}`
	if got.Artifact != want {
		t.Errorf("artifact = %q, want %q", got.Artifact, want)
	}
	if gotModel != "router" {
		t.Errorf("model = %q, want router", gotModel)
	}
	for _, s := range []string{"billing", "refund", "process a refund request", "money back"} {
		if !strings.Contains(gotPrompt, s) {
			t.Errorf("prompt missing %q:\n%s", s, gotPrompt)
		}
	}
}

func TestRouterNoMatchReturnsNull(t *testing.T) {
	for _, reply := range []string{"0", "3", "-1", "none", "docs/search"} {
		srv := stubProxy(t, reply, nil, nil)
		got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: testInput})
		if got.Artifact != "null" || !got.Success {
			t.Errorf("reply %q: got %+v, want null/success", reply, got)
		}
	}
}

func TestRouterJunkOutputIsNullNotPanic(t *testing.T) {
	for _, reply := range []string{"", "   ", "\x00\xff{{[", "I think maybe 1 or 2", "1 2", "99999999999999999999999"} {
		srv := stubProxy(t, reply, nil, nil)
		got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: testInput})
		if got.Artifact != "null" || !got.Success {
			t.Errorf("reply %q: got %+v, want null/success", reply, got)
		}
	}
}

func TestRouterAcceptsDecoratedNumber(t *testing.T) {
	for _, reply := range []string{" 1 ", "1.", "(1)", "\"1\"", "1\n"} {
		srv := stubProxy(t, reply, nil, nil)
		got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: testInput})
		if got.Artifact != `{"role":"billing","workflow":"answer"}` {
			t.Errorf("reply %q: got %+v", reply, got)
		}
	}
}

func TestRouterMalformedInputFails(t *testing.T) {
	srv := stubProxy(t, "1", nil, nil)
	for _, in := range []string{"", "not json", `[1,2]`, `{"menu":"x"}`} {
		got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: in})
		if got.Success || got.Reason == "" {
			t.Errorf("input %q: got %+v, want failure with reason", in, got)
		}
	}
}

func TestRouterEmptyMenuIsNull(t *testing.T) {
	srv := stubProxy(t, "1", nil, nil)
	got := classify(context.Background(), srv.Client(), srv.URL, "tok",
		agentkit.Request{Input: `{"menu":[],"utterance":"hi"}`})
	if got.Artifact != "null" || !got.Success {
		t.Errorf("got %+v, want null/success", got)
	}
}

func TestRouterProxyErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	got := classify(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: testInput})
	if got.Success || got.Reason == "" || got.Artifact != "" {
		t.Errorf("got %+v, want failure with reason and no artifact", got)
	}
}
