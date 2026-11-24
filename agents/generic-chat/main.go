// Command generic-chat answers a prompt from the model's own knowledge over
// the chat door. It has no tools and no other capabilities.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/agenthof/hub/internal/agentkit"
)

// model is the logical model name the operator maps to a real model in the
// gateway catalog.
const model = "chat"

// answer sends the request input as a single user message and maps the reply
// to a result. It is separate from the Serve callback so tests can drive it
// against a stand-in proxy.
func answer(ctx context.Context, client *http.Client, proxyURL, runToken string, req agentkit.Request) agentkit.Result {
	reply, err := agentkit.ChatOnce(ctx, client, proxyURL, runToken, model,
		[]agentkit.Msg{{Role: "user", Content: req.Input}})
	if err != nil {
		return agentkit.Result{Success: false, Reason: err.Error()}
	}
	return agentkit.Result{Artifact: reply, Success: true}
}

func main() {
	socket := flag.String("socket", "", "unix socket path to serve on")
	flag.Parse()
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "generic-chat: -socket is required")
		os.Exit(2)
	}
	err := agentkit.Serve(*socket, func(ctx context.Context, proxyURL, runToken string, req agentkit.Request) agentkit.Result {
		return answer(ctx, http.DefaultClient, proxyURL, runToken, req)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "generic-chat:", err)
		os.Exit(1)
	}
}
