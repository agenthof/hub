// Command rag-ingest indexes meeting notes into the governed memory store. It
// searches the Drive tool door, reads each note, splits it into chunks that
// never cut a sentence, embeds every chunk through the embeddings door and
// upserts it into the memory store. It makes no chat call.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agenthof/hub/internal/agentkit"
)

const (
	// collection is the single memory collection shared by every rag agent.
	collection = "notes"
	// embedModel is the logical embedding model the operator maps to a real one.
	embedModel = "embed"
	// driveQuery is the fixed broad query that finds the meeting notes.
	driveQuery = "sync"
	// maxChunk caps a chunk's size in bytes, well under the store's limit.
	maxChunk = 8000
	// headerPrefix starts the first line of a note: "Meeting notes - YYYY-MM-DD".
	headerPrefix = "Meeting notes - "
)

// parseDate returns the meeting date from the first line of body, or "" when
// the header is absent or the date does not parse (an undated note).
func parseDate(body string) string {
	first, _, _ := strings.Cut(body, "\n")
	first = strings.TrimSpace(first)
	rest, ok := strings.CutPrefix(first, headerPrefix)
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	if _, err := time.Parse("2006-01-02", rest); err != nil {
		return ""
	}
	return rest
}

// chunk splits body on blank-line (paragraph) boundaries, one chunk per
// paragraph. A paragraph longer than max is split on sentence boundaries, so a
// sentence is never cut; only a single sentence longer than max is split, at
// whitespace, as a last resort. Empty chunks are dropped.
func chunk(body string, max int) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var out []string
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if len(para) <= max {
			out = append(out, para)
			continue
		}
		out = append(out, packSentences(para, max)...)
	}
	return out
}

// packSentences greedily packs whole sentences into chunks of at most max bytes.
func packSentences(para string, max int) []string {
	var out []string
	var cur string
	flush := func() {
		if cur != "" {
			out = append(out, cur)
			cur = ""
		}
	}
	for _, s := range sentences(para) {
		if len(s) > max {
			flush()
			out = append(out, splitWords(s, max)...)
			continue
		}
		if cur != "" && len(cur)+1+len(s) > max {
			flush()
		}
		if cur == "" {
			cur = s
		} else {
			cur += " " + s
		}
	}
	flush()
	return out
}

// sentences splits text after ". ", "! ", "? " or a newline.
func sentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		end := false
		switch c {
		case '.', '!', '?':
			end = i+1 == len(text) || text[i+1] == ' ' || text[i+1] == '\n'
		case '\n':
			end = true
		}
		if end {
			if s := strings.TrimSpace(text[start : i+1]); s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// splitWords splits one over-long sentence at whitespace into pieces of at
// most max bytes (a word longer than max is cut at max).
func splitWords(s string, max int) []string {
	var out []string
	var cur string
	for _, w := range strings.Fields(s) {
		for len(w) > max {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			out = append(out, w[:max])
			w = w[max:]
		}
		if cur != "" && len(cur)+1+len(w) > max {
			out = append(out, cur)
			cur = ""
		}
		if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// ingest indexes every note the Drive query finds and reports
// "indexed N chunks (M undated)". N counts chunks upserted across all notes;
// M counts CHUNKS (not notes) whose note has no parseable date. It is separate
// from the Serve callback so tests can drive it against a stand-in proxy.
func ingest(ctx context.Context, client *http.Client, proxyURL, runToken string, _ agentkit.Request) agentkit.Result {
	fail := func(format string, a ...any) agentkit.Result {
		return agentkit.Result{Success: false, Reason: fmt.Sprintf(format, a...)}
	}
	tc := agentkit.NewToolClient(client, proxyURL, runToken)
	hits, err := tc.DriveSearch(ctx, driveQuery)
	if err != nil {
		return fail("drive search: %v", err)
	}
	total, undated := 0, 0
	for _, hit := range hits {
		body, err := tc.DriveRead(ctx, hit.ID)
		if err != nil {
			return fail("drive read %s: %v", hit.ID, err)
		}
		date := parseDate(body)
		for i, text := range chunk(body, maxChunk) {
			vec, err := agentkit.EmbedOnce(ctx, client, proxyURL, runToken, embedModel, text)
			if err != nil {
				return fail("embed %s#%d: %v", hit.ID, i, err)
			}
			id := fmt.Sprintf("%s#%d", hit.ID, i)
			md := map[string]any{"source": hit.ID, "chunk_index": i, "date": date}
			if _, err := tc.MemoryUpsert(ctx, collection, id, text, vec, md); err != nil {
				return fail("upsert %s: %v", id, err)
			}
			total++
			if date == "" {
				undated++
			}
		}
	}
	return agentkit.Result{Artifact: fmt.Sprintf("indexed %d chunks (%d undated)", total, undated), Success: true}
}

func main() {
	socket := flag.String("socket", "", "unix socket path to serve on")
	flag.Parse()
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "rag-ingest: -socket is required")
		os.Exit(2)
	}
	err := agentkit.Serve(*socket, func(ctx context.Context, proxyURL, runToken string, req agentkit.Request) agentkit.Result {
		return ingest(ctx, http.DefaultClient, proxyURL, runToken, req)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "rag-ingest:", err)
		os.Exit(1)
	}
}
