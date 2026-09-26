package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikelear/leartech-go-common/pkg/aigateway"

	"github.com/mikelear/leartech-ship-proven/pkg/agentrun"
)

// THE BINARY HAS TO BE DRIVEN THROUGH ITS OWN ARGV PATH.
//
// Testing agentrun.Run directly says nothing about whether the flags reach
// it, whether the exit codes are right, or whether the run is wired to
// stdout and stderr at all — and in leartech-ba-service the equivalent tests
// all called the command function directly, so deleting its registration
// from main's dispatch left every one of them green while the binary
// answered "unknown command". These go through run(), which is what main
// calls.

func gateway(t *testing.T, reply string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", chunk(map[string]any{"role": "assistant"}, "", nil))
		fmt.Fprintf(w, "data: %s\n\n", chunk(map[string]any{"content": reply}, "", nil))
		fmt.Fprintf(w, "data: %s\n\n", chunk(map[string]any{}, "stop",
			map[string]any{"prompt_tokens": 9, "completion_tokens": 3, "total_tokens": 12}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func chunk(delta map[string]any, finish string, usage map[string]any) string {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	}
	rec := map[string]any{"id": "x", "object": "chat.completion.chunk", "created": 1,
		"model": "echo", "choices": []map[string]any{choice}}
	if usage != nil {
		rec["usage"] = usage
	}
	b, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func env(t *testing.T, gw string) {
	t.Helper()
	t.Setenv(aigateway.EnvURL, gw)
	t.Setenv(aigateway.EnvAPIKey, "sk-lt-minted-per-run")
	t.Setenv(aigateway.EnvModel, "echo")
	t.Setenv(aigateway.EnvRunID, "agentrun-abc123")
	t.Setenv(aigateway.EnvSessionID, "")
	t.Setenv(agentrun.EnvBrief, "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_BASE_URL", "")
}

// capture runs the binary's real entrypoint against temp files, because run()
// writes to *os.File rather than io.Writer — which is deliberate: stdout and
// stderr ARE files in a Job, and an interface here would let a test pass
// against something a pod cannot produce.
func capture(t *testing.T, argv ...string) (out, errOut string, code int) {
	t.Helper()
	dir := t.TempDir()
	so, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	se, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	code = run(argv, so, se)
	_ = so.Close()
	_ = se.Close()
	ob, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	eb, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	return string(ob), string(eb), code
}

// THE ORDINARY RUN. A brief from the environment, an answer on stdout, the
// structured record on stderr, and exit 0.
func TestRun_AnswersAndSeparatesTheAnswerFromTheRecord(t *testing.T) {
	env(t, gateway(t, "there are three files"))
	t.Setenv(agentrun.EnvBrief, "summarise what is here")
	root := t.TempDir()

	out, errOut, code := capture(t, "--read-only", "--root", root)

	if code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "three files") {
		t.Errorf("the answer did not reach stdout: %q", out)
	}
	// THE SPLIT IS THE CONTRACT: a Job capturing stdout gets the answer and
	// nothing else.
	if strings.Contains(out, "agent_start") || strings.Contains(out, "run_id") {
		t.Errorf("the structured record leaked onto stdout: %q", out)
	}
	if !strings.Contains(errOut, `"event":"agent_end"`) {
		t.Errorf("no record on stderr: %q", errOut)
	}
}

// A MISSING CREDENTIAL IS NOT A FAILED BRIEF. The controller reads the exit
// code, and "my Secret did not project" wants a different remedy from "the
// work did not succeed".
func TestRun_AMissingCredentialExitsDistinctly(t *testing.T) {
	env(t, gateway(t, "x"))
	t.Setenv(agentrun.EnvBrief, "do the thing")
	t.Setenv(aigateway.EnvAPIKey, "")

	_, errOut, code := capture(t, "--read-only", "--root", t.TempDir())

	if code != exitNoConfig {
		t.Errorf("exit = %d, want %d (no config) — collapsing this into the "+
			"generic failure code makes a projection bug look like a bad brief",
			code, exitNoConfig)
	}
	if !strings.Contains(errOut, aigateway.EnvAPIKey) {
		t.Errorf("the refusal must name the projected variable: %q", errOut)
	}
}

// AN ABSENT BRIEF IS A USAGE ERROR, and nothing reaches the gateway.
func TestRun_AnAbsentBriefIsAUsageError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	env(t, srv.URL)

	_, errOut, code := capture(t, "--read-only", "--root", t.TempDir())

	if code != exitBadUsage {
		t.Errorf("exit = %d, want %d", code, exitBadUsage)
	}
	if calls != 0 {
		t.Errorf("the gateway was called %d time(s) for a run with no brief", calls)
	}
	if !strings.Contains(errOut, agentrun.EnvBrief) {
		t.Errorf("the refusal must say where a brief can come from: %q", errOut)
	}
}

// A MISSING --brief FILE FAILS BEFORE ANYTHING COSTS, and names the path.
func TestRun_AMissingBriefFileNamesThePath(t *testing.T) {
	env(t, gateway(t, "x"))
	missing := filepath.Join(t.TempDir(), "nope.txt")

	_, errOut, code := capture(t, "--brief", missing, "--root", t.TempDir())

	if code != exitBadUsage {
		t.Errorf("exit = %d, want %d", code, exitBadUsage)
	}
	if !strings.Contains(errOut, "nope.txt") {
		t.Errorf("the path is not named: %q", errOut)
	}
}

// --brief WINS OVER THE ENVIRONMENT, so a Job given a file does not silently
// run an inherited brief.
func TestRun_TheBriefFlagWinsOverTheEnvironment(t *testing.T) {
	env(t, gateway(t, "ok"))
	t.Setenv(agentrun.EnvBrief, "the inherited brief")
	f := filepath.Join(t.TempDir(), "brief.txt")
	if err := os.WriteFile(f, []byte("the explicit brief"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := capture(t, "--brief", f, "--read-only", "--root", t.TempDir())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	// brief_bytes on the start line is how long the brief was.
	if !strings.Contains(errOut, `"brief_bytes":18`) {
		t.Errorf("the explicit brief was not the one used: %q", errOut)
	}
}

// A FAILED RUN EXITS 1 AND STILL REPORTS WHAT IT SPENT.
func TestRun_AFailedRunExitsOneAndStillReports(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	env(t, srv.URL)
	t.Setenv(agentrun.EnvBrief, "do the thing")

	_, errOut, code := capture(t, "--read-only", "--root", t.TempDir())

	if code != exitFailed {
		t.Errorf("exit = %d, want %d", code, exitFailed)
	}
	if !strings.Contains(errOut, "agent_usage_total") {
		t.Errorf("no total on a failed run: %q", errOut)
	}
	if !strings.Contains(errOut, `"ok":false`) {
		t.Errorf("the end line does not record the failure: %q", errOut)
	}
}

// --read-only IS REACHED BY THE FLAG, not merely available in the library.
func TestRun_TheReadOnlyFlagReachesTheRun(t *testing.T) {
	env(t, gateway(t, "ok"))
	t.Setenv(agentrun.EnvBrief, "look around")
	root := t.TempDir()

	_, ro, _ := capture(t, "--read-only", "--root", root)
	if !strings.Contains(ro, `"read_only":true`) {
		t.Errorf("--read-only did not reach the run: %q", ro)
	}
	if strings.Contains(ro, "local__write_file") {
		t.Error("a read-only run advertised the writing tool")
	}

	_, rw, _ := capture(t, "--root", root)
	if !strings.Contains(rw, `"read_only":false`) {
		t.Errorf("the default is not a writing run: %q", rw)
	}
	if !strings.Contains(rw, "local__write_file") {
		t.Errorf("a writing run did not advertise the writing tool: %q", rw)
	}
}

// --version PRINTS AND EXITS, without needing a gateway. A pod whose image is
// wrong is diagnosed with this and nothing else.
func TestRun_VersionNeedsNoCredential(t *testing.T) {
	t.Setenv(aigateway.EnvURL, "")
	t.Setenv(aigateway.EnvAPIKey, "")

	out, _, code := capture(t, "--version")
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("--version printed nothing")
	}
}

// --transcript WRITES THE LOOP'S EVENTS, and an unwritable path fails before
// anything costs.
func TestRun_TheTranscriptIsWrittenAndAnUnwritablePathFailsEarly(t *testing.T) {
	env(t, gateway(t, "ok"))
	t.Setenv(agentrun.EnvBrief, "look around")
	tr := filepath.Join(t.TempDir(), "run.jsonl")

	if _, errOut, code := capture(t, "--read-only", "--root", t.TempDir(), "--transcript", tr); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	b, err := os.ReadFile(tr)
	if err != nil || len(b) == 0 {
		t.Fatalf("the transcript is empty or missing: %v", err)
	}

	bad := filepath.Join(t.TempDir(), "no-such-dir", "run.jsonl")
	if _, _, code := capture(t, "--read-only", "--root", t.TempDir(), "--transcript", bad); code != exitBadUsage {
		t.Errorf("an unwritable transcript exited %d, want %d", code, exitBadUsage)
	}
}

// AN UNKNOWN FLAG IS A USAGE ERROR rather than a silent default. A Job spec
// with a typo should fail loudly at once.
func TestRun_AnUnknownFlagIsAUsageError(t *testing.T) {
	env(t, gateway(t, "ok"))
	if _, _, code := capture(t, "--not-a-flag"); code != exitBadUsage {
		t.Errorf("exit = %d, want %d", code, exitBadUsage)
	}
}
