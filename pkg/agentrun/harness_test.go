package agentrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

// fakeGateway streams one assistant turn and records what it was sent.
//
// SSE, NOT A PLAIN JSON BODY. agentloop.Runner drives a Streamer, so a fake
// answering a single JSON object produces "the stream ended without [DONE];
// the turn is incomplete" — the client is right and the fake is wrong. The
// frame shape is the gateway's own: `data: %s\n\n` per chunk and a literal
// `data: [DONE]\n\n` to close.
func fakeGateway(t *testing.T, reply string) (url string, seen func() []*http.Request) {
	t.Helper()
	var got []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		got = append(got, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", frame(map[string]any{"role": "assistant"}, ""))
		fmt.Fprintf(w, "data: %s\n\n", frame(map[string]any{"content": reply}, ""))
		fmt.Fprintf(w, "data: %s\n\n", frameWithUsage(map[string]any{}, "stop",
			map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []*http.Request { return got }
}

// deadGateway answers with no frames at all: the turn fails.
func deadGateway(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// silentGateway answers a full turn but reports NO usage, which is what a
// supplier with no metering does. Its own test exists because the total line
// counted turns from the usage callback, so a run like this reported turns:0
// for work that demonstrably happened.
func silentGateway(t *testing.T, reply string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", frame(map[string]any{"role": "assistant"}, ""))
		fmt.Fprintf(w, "data: %s\n\n", frame(map[string]any{"content": reply}, ""))
		fmt.Fprintf(w, "data: %s\n\n", frame(map[string]any{}, "stop"))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func frame(delta map[string]any, finish string) string {
	return frameWithUsage(delta, finish, nil)
}

func frameWithUsage(delta map[string]any, finish string, usage map[string]any) string {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	rec := map[string]any{
		"id": "x", "object": "chat.completion.chunk", "created": 1,
		"model": "echo", "choices": []map[string]any{choice},
	}
	if usage != nil {
		rec["usage"] = usage
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// controllerEnv sets exactly what the orchestrator-controller projects into
// an agent Job, and clears the provider-native variables the library refuses.
func controllerEnv(t *testing.T, gateway string) {
	t.Helper()
	t.Setenv(aigateway.EnvURL, gateway)
	t.Setenv(aigateway.EnvAPIKey, "sk-lt-minted-per-run")
	t.Setenv(aigateway.EnvModel, "echo")
	t.Setenv(aigateway.EnvRunID, "agentrun-abc123")
	t.Setenv(aigateway.EnvSessionID, "")
	t.Setenv(EnvBrief, "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
}

// logLines decodes the structured log a run emitted.
func logLines(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func eventNamed(recs []map[string]any, name string) map[string]any {
	for _, r := range recs {
		if r["event"] == name {
			return r
		}
	}
	return nil
}
