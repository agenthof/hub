// Package agentkit is a small, dependency-free helper for agents that run
// behind the engine's fronted contract: it serves the per-step HTTP request on
// a unix socket and makes a single model call through the per-run proxy.
package agentkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

const (
	headerProxyURL = "X-Agenthof-Proxy-URL"
	headerRunToken = "X-Agenthof-Run-Token"

	maxRequestBytes  = 1 << 20
	maxResponseBytes = 8 << 20
)

// Msg is one chat message.
type Msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is the per-step input the engine posts to the agent.
type Request struct {
	Input     string            `json:"input"`
	Artifacts map[string]string `json:"artifacts"`
	Agent     string            `json:"agent"`
}

// Result is what the agent returns for a step.
type Result struct {
	Artifact string `json:"artifact"`
	Success  bool   `json:"success"`
	Reason   string `json:"reason"`
}

// Handler runs one step. proxyURL and runToken are the per-run gateway
// coordinates the engine supplied with the request.
type Handler func(ctx context.Context, proxyURL, runToken string, req Request) Result

func handlerFor(h Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes)).Decode(&req); err != nil {
			http.Error(w, "bad request body", http.StatusBadRequest)
			return
		}
		proxyURL := r.Header.Get(headerProxyURL)
		runToken := r.Header.Get(headerRunToken)
		var res Result
		if proxyURL == "" || runToken == "" {
			res = Result{Reason: "missing proxy URL or run token headers"}
		} else {
			res = h(r.Context(), proxyURL, runToken, req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
}

// Serve listens on the unix socket at socketPath and serves the fronted
// contract, calling h once per POST. It blocks until the listener fails.
func Serve(socketPath string, h Handler) error {
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handlerFor(h)}
	return srv.Serve(ln)
}

const unixScheme = "unix://"

// clientAndBase returns the client and URL base for a proxy URL. An http(s)
// proxy uses a copy of the caller's client; a unix:// proxy gets a per-call
// client that dials the socket, with a placeholder host. Neither follows
// redirects.
func clientAndBase(client *http.Client, proxyURL string) (*http.Client, string) {
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if path, ok := strings.CutPrefix(proxyURL, unixScheme); ok {
		return &http.Client{
			CheckRedirect: noRedirect,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", path)
				},
			},
		}, "http://agenthof"
	}
	c := http.Client{}
	if client != nil {
		c = *client
	}
	c.CheckRedirect = noRedirect
	return &c, strings.TrimRight(proxyURL, "/")
}

// ChatOnce makes one chat completion call through the per-run proxy and
// returns the first choice's message content. Redirects are never followed:
// the proxy is a trust boundary.
func ChatOnce(ctx context.Context, client *http.Client, proxyURL, runToken, model string, messages []Msg) (string, error) {
	c, base := clientAndBase(client, proxyURL)

	body, err := json.Marshal(struct {
		Model    string `json:"model"`
		Messages []Msg  `json:"messages"`
	}{model, messages})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+runToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("chat completion: status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("chat completion: decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("chat completion: no choices")
	}
	return out.Choices[0].Message.Content, nil
}
