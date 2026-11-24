// Command router-chat classifies a user utterance against a menu of workflows
// it is told about, over the chat door. It picks at most one menu entry and
// reports it; it spawns nothing and has no tools or other capabilities.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/agenthof/hub/internal/agentkit"
)

// model is the logical model name the operator maps to a real model in the
// gateway catalog.
const model = "router"

type entry struct {
	Role        string `json:"role"`
	Workflow    string `json:"workflow"`
	Description string `json:"description"`
}

type input struct {
	Menu      []entry `json:"menu"`
	Utterance string  `json:"utterance"`
}

// pick is the emitted artifact: only the identifying pair, never the model's
// own text.
type pick struct {
	Role     string `json:"role"`
	Workflow string `json:"workflow"`
}

func parseInput(raw string) (input, error) {
	var in input
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return input{}, fmt.Errorf("input must be JSON {\"menu\":[...],\"utterance\":\"...\"}: %w", err)
	}
	if in.Menu == nil {
		return input{}, errors.New("input has no menu")
	}
	return in, nil
}

// prompt numbers the menu entries so the model answers with a single number.
func prompt(in input) string {
	var b strings.Builder
	b.WriteString("Choose the one workflow below that best handles the user's request.\n")
	b.WriteString("Reply with only the number of the workflow, or 0 if none fits.\n\n")
	for i, e := range in.Menu {
		fmt.Fprintf(&b, "%d. role=%s workflow=%s: %s\n", i+1, e.Role, e.Workflow, e.Description)
	}
	b.WriteString("\nUser request:\n")
	b.WriteString(in.Utterance)
	return b.String()
}

// resolve maps the model's reply to a menu index. The reply must be a single
// number (optionally wrapped in quotes, brackets or a trailing period) naming
// an existing entry; anything else is no match.
func resolve(reply string, n int) (int, bool) {
	s := strings.TrimSpace(reply)
	s = strings.Trim(s, "\"'`()[]")
	s = strings.TrimSuffix(s, ".")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 || v > n {
		return 0, false
	}
	return v - 1, true
}

// classify asks the model to pick a menu entry and emits that entry or null.
// It is separate from the Serve callback so tests can drive it against a
// stand-in proxy.
func classify(ctx context.Context, client *http.Client, proxyURL, runToken string, req agentkit.Request) agentkit.Result {
	in, err := parseInput(req.Input)
	if err != nil {
		return agentkit.Result{Success: false, Reason: err.Error()}
	}
	if len(in.Menu) == 0 {
		return agentkit.Result{Artifact: "null", Success: true}
	}
	reply, err := agentkit.ChatOnce(ctx, client, proxyURL, runToken, model,
		[]agentkit.Msg{{Role: "user", Content: prompt(in)}})
	if err != nil {
		return agentkit.Result{Success: false, Reason: err.Error()}
	}
	i, ok := resolve(reply, len(in.Menu))
	if !ok {
		return agentkit.Result{Artifact: "null", Success: true}
	}
	out, err := json.Marshal(pick{Role: in.Menu[i].Role, Workflow: in.Menu[i].Workflow})
	if err != nil {
		return agentkit.Result{Success: false, Reason: err.Error()}
	}
	return agentkit.Result{Artifact: string(out), Success: true}
}

func main() {
	socket := flag.String("socket", "", "unix socket path to serve on")
	flag.Parse()
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "router-chat: -socket is required")
		os.Exit(2)
	}
	err := agentkit.Serve(*socket, func(ctx context.Context, proxyURL, runToken string, req agentkit.Request) agentkit.Result {
		return classify(ctx, http.DefaultClient, proxyURL, runToken, req)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "router-chat:", err)
		os.Exit(1)
	}
}
