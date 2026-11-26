package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// startServe runs Serve on a short unix socket path and returns an HTTP
// client that dials it.
func startServe(t *testing.T, h Handler) *http.Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ak")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "a.sock")
	go func() { _ = Serve(sock, h) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

func TestServeRejectsNonPost(t *testing.T) {
	c := startServe(t, func(context.Context, string, string, Request) Result {
		t.Error("handler must not be called")
		return Result{}
	})
	resp, err := c.Get("http://agent/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestServeMissingProxyCoords(t *testing.T) {
	called := false
	c := startServe(t, func(context.Context, string, string, Request) Result {
		called = true
		return Result{Success: true}
	})
	resp, err := c.Post("http://agent/", "application/json", bytes.NewReader([]byte(`{"input":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got Result
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Success || got.Reason == "" {
		t.Fatalf("got %+v, want failure with a reason", got)
	}
	if called {
		t.Fatal("handler was called without proxy coordinates")
	}
}

func TestServeHappyPathCallsHandler(t *testing.T) {
	want := Request{Input: "in", Artifacts: map[string]string{"a": "b"}, Agent: "ag"}
	var gotURL, gotTok string
	var gotReq Request
	c := startServe(t, func(_ context.Context, u, tok string, r Request) Result {
		gotURL, gotTok, gotReq = u, tok, r
		return Result{Artifact: "out", Success: true, Reason: "ok"}
	})
	body, _ := json.Marshal(want)
	req, _ := http.NewRequest(http.MethodPost, "http://agent/", bytes.NewReader(body))
	req.Header.Set("X-Agenthof-Proxy-URL", "http://proxy:1")
	req.Header.Set("X-Agenthof-Run-Token", "tok")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got Result
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if gotURL != "http://proxy:1" || gotTok != "tok" || !reflect.DeepEqual(gotReq, want) {
		t.Fatalf("handler saw %q %q %+v", gotURL, gotTok, gotReq)
	}
	if got != (Result{Artifact: "out", Success: true, Reason: "ok"}) {
		t.Fatalf("result = %+v", got)
	}
}

func TestChatOnceSendsModelAndBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if a := r.Header.Get("Authorization"); a != "Bearer tok" {
			t.Errorf("Authorization = %q", a)
		}
		var b struct {
			Model    string `json:"model"`
			Messages []Msg  `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if b.Model != "m" || len(b.Messages) != 1 || b.Messages[0] != (Msg{Role: "user", Content: "hi"}) {
			t.Errorf("body = %+v", b)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer srv.Close()
	got, err := ChatOnce(context.Background(), srv.Client(), srv.URL, "tok", "m", []Msg{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestChatOnceNon2xxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := ChatOnce(context.Background(), srv.Client(), srv.URL, "tok", "m", nil); err == nil {
		t.Fatal("want error on 500")
	}
}

func TestChatOnceDoesNotFollowRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("redirect target must not be contacted")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"}}]}`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := ChatOnce(context.Background(), srv.Client(), srv.URL, "tok", "m", nil); err == nil {
		t.Fatal("want error on redirect")
	}
}

func serveUnix(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ak")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "gw.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "unix://" + sock
}

func TestChatOnceOverUnixSocket(t *testing.T) {
	u := serveUnix(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if a := r.Header.Get("Authorization"); a != "Bearer tok" {
			t.Errorf("Authorization = %q", a)
		}
		var b struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		if b.Model != "m" {
			t.Errorf("model = %q", b.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"over-unix"}}]}`))
	}))
	got, err := ChatOnce(context.Background(), nil, u, "tok", "m", []Msg{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if got != "over-unix" {
		t.Fatalf("got %q", got)
	}
}

func TestChatOnceOverUnixSocketNon2xxErrors(t *testing.T) {
	u := serveUnix(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	if _, err := ChatOnce(context.Background(), nil, u, "tok", "m", nil); err == nil {
		t.Fatal("want error on 502")
	}
}

func TestEmbedOnceSendsModelAndParsesVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth = %q", got)
		}
		var body struct{ Model, Input string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "embed" || body.Input != "hello" {
			t.Errorf("body = %+v", body)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.5,-1,2.25]}]}`))
	}))
	defer srv.Close()
	got, err := EmbedOnce(context.Background(), srv.Client(), srv.URL+"/", "tok", "embed", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []float32{0.5, -1, 2.25}) {
		t.Fatalf("got %v", got)
	}
}

func TestEmbedOnceErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"non2xx":    func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", http.StatusBadGateway) },
		"empty":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[]}`)) },
		"badjson":   func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`nope`)) },
		"redirect":  func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) },
		"noembeddn": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"data":[{"embedding":[]}]}`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			if _, err := EmbedOnce(context.Background(), srv.Client(), srv.URL, "tok", "embed", "x"); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestEmbedOnceOverUnixSocket(t *testing.T) {
	u := serveUnix(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,2]}]}`))
	}))
	got, err := EmbedOnce(context.Background(), nil, u, "tok", "embed", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []float32{1, 2}) {
		t.Fatalf("got %v", got)
	}
}
