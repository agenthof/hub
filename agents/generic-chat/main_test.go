package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agenthof/hub/internal/agentkit"
)

func TestGenericChatReturnsReply(t *testing.T) {
	var gotModel, gotAuth string
	var gotMsgs []agentkit.Msg
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string         `json:"model"`
			Messages []agentkit.Msg `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, gotMsgs, gotAuth = body.Model, body.Messages, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer srv.Close()

	got := answer(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: "hi"})
	want := agentkit.Result{Artifact: "hello", Success: true}
	if got != want {
		t.Fatalf("result = %+v, want %+v", got, want)
	}
	if gotModel != "chat" {
		t.Errorf("model = %q, want chat", gotModel)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if len(gotMsgs) != 1 || gotMsgs[0] != (agentkit.Msg{Role: "user", Content: "hi"}) {
		t.Errorf("messages = %+v", gotMsgs)
	}
}

func TestGenericChatProxyErrorFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	got := answer(context.Background(), srv.Client(), srv.URL, "tok", agentkit.Request{Input: "hi"})
	if got.Success {
		t.Fatalf("expected failure, got %+v", got)
	}
	if got.Reason == "" {
		t.Error("expected a non-empty reason")
	}
	if got.Artifact != "" {
		t.Errorf("artifact = %q, want empty", got.Artifact)
	}
}
