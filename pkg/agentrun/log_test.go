package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

func fixedLog(out *bytes.Buffer, runID string) *runLog {
	l := newRunLog(out, runID)
	l.now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	return l
}

// EVERY LINE CARRIES THE RUN ID. It is the join key the gateway's usage rows,
// lokiserver's run_report and the recorder all correlate on, so a line
// without one is a line nothing can attribute.
func TestRunLog_EveryLineCarriesTheRunID(t *testing.T) {
	var b bytes.Buffer
	l := fixedLog(&b, "agentrun-xyz")
	l.event("agent_start", map[string]any{"model": "echo"})
	l.usage(aigateway.ChatUsage{PromptTokens: 11, CompletionTokens: 5})
	l.total(&meter{turns: 1, prompt: 11, completion: 5}, 0, nil)
	l.event("agent_end", map[string]any{"ok": true})

	recs := logLines(t, &b)
	if len(recs) != 4 {
		t.Fatalf("got %d lines, want 4:\n%s", len(recs), b.String())
	}
	for i, rec := range recs {
		if rec["run_id"] != "agentrun-xyz" {
			t.Errorf("line %d has run_id %v; Loki cannot attribute it", i, rec["run_id"])
		}
		if rec["event"] == nil || rec["event"] == "" {
			t.Errorf("line %d has no event name", i)
		}
		if rec["ts"] == nil {
			t.Errorf("line %d has no timestamp", i)
		}
	}
}

// ONE JSON OBJECT PER LINE, which is what Loki's `| json` parser needs. A
// console-style line breaks the parser and the contract fields stop being
// queryable — the defect the controller's LOG_FORMAT default fixed.
func TestRunLog_WritesOneJSONObjectPerLine(t *testing.T) {
	var b bytes.Buffer
	l := fixedLog(&b, "r")
	l.usage(aigateway.ChatUsage{PromptTokens: 11})
	l.total(&meter{turns: 2}, 0, nil)

	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("not a bare JSON object: %q", line)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("unparseable: %v", err)
		}
	}
}

// A LOG LINE THAT CANNOT BE ENCODED MUST NOT TAKE THE RUN OUT, and must not
// vanish either: a run lost to its own instrumentation is the worst of both.
func TestRunLog_AnUnencodableFieldDoesNotStopTheRun(t *testing.T) {
	var b bytes.Buffer
	fixedLog(&b, "r1").event("agent_start", map[string]any{"bad": make(chan int)})

	out := b.String()
	if out == "" {
		t.Fatal("nothing was written; the failure was swallowed entirely, so the " +
			"run would lose the line AND any trace that it existed")
	}
	if !strings.Contains(out, "r1") {
		t.Errorf("the fallback dropped the run id: %q", out)
	}
	if !strings.Contains(out, "log_error") {
		t.Errorf("the fallback does not say why it is degraded: %q", out)
	}
}

// THE CONVENTION IS NAMED. The gateway's own log is ADDITIVE and this line is
// SUBSET; mixing them yields a cache hit rate over 100%, and a reader
// reconciling the two observers cannot tell which they hold unless it says.
func TestRunLog_NamesTheTokenConvention(t *testing.T) {
	var b bytes.Buffer
	fixedLog(&b, "r").usage(aigateway.ChatUsage{PromptTokens: 11, CompletionTokens: 5})

	rec := logLines(t, &b)[0]
	if rec["prompt_tokens_convention"] != "subset" {
		t.Errorf("convention = %v, want subset", rec["prompt_tokens_convention"])
	}
}

// ABSENT IS NOT ZERO. A supplier with no prompt cache reports nothing about
// one; writing 0 calls that a total miss and invents a denominator.
func TestRunLog_ASupplierWithNoCacheReportsNoCacheFields(t *testing.T) {
	var b bytes.Buffer
	fixedLog(&b, "r").usage(aigateway.ChatUsage{PromptTokens: 11, CompletionTokens: 5})

	rec := logLines(t, &b)[0]
	if _, ok := rec["cache_read_tokens"]; ok {
		t.Error("cache_read_tokens is present for a supplier that reported no " +
			"cache; 0 there would read as a total miss")
	}
}

// THE SAME RULE ON THE TOTAL. The turn count comes from the usage callback,
// so a supplier with no metering left it at 0 — which reads as "this run
// never called a model" for work that demonstrably happened.
func TestRunLog_TheTotalOmitsFiguresWhenNothingWasMetered(t *testing.T) {
	var b bytes.Buffer
	fixedLog(&b, "r").total(&meter{}, 0, nil)

	rec := logLines(t, &b)[0]
	if rec["usage_reported"] != false {
		t.Errorf("usage_reported = %v, want false", rec["usage_reported"])
	}
	if _, ok := rec["turns"]; ok {
		t.Error("turns is present with nothing metered; 0 there is a claim about " +
			"the run rather than a gap")
	}
	if rec["note"] == nil || !strings.Contains(rec["note"].(string), "absent rather than zero") {
		t.Errorf("the note must say which case this is: %v", rec["note"])
	}

	// And a metered run DOES carry them, or the check above passes against a
	// total that never reports anything.
	var b2 bytes.Buffer
	fixedLog(&b2, "r").total(&meter{turns: 3, prompt: 30}, 0, nil)
	rec2 := logLines(t, &b2)[0]
	if rec2["usage_reported"] != true || rec2["turns"] != float64(3) {
		t.Errorf("a metered run reported %v", rec2)
	}
}

// REFUSALS ARE PART OF THE ACCOUNT. A run declined forty tool calls and then
// reporting it could not finish looks like a model problem; the count says it
// was a permission one.
func TestRunLog_TheTotalCarriesRefusals(t *testing.T) {
	var b bytes.Buffer
	fixedLog(&b, "r").total(&meter{turns: 1}, 3, []string{"local__bash", "local__write_file"})

	rec := logLines(t, &b)[0]
	if rec["tool_calls_refused"] != float64(3) {
		t.Errorf("tool_calls_refused = %v, want 3", rec["tool_calls_refused"])
	}
	if rec["tools_refused"] == nil {
		t.Error("the refused tools are not named")
	}

	// Absent when there were none, so the field means something when it
	// appears rather than being noise on every run.
	var clean bytes.Buffer
	fixedLog(&clean, "r").total(&meter{turns: 1}, 0, nil)
	if _, ok := logLines(t, &clean)[0]["tool_calls_refused"]; ok {
		t.Error("tool_calls_refused appeared with nothing refused")
	}
}

// A nil Log WRITER IS NOT A PANIC. A caller that wants no structured output
// is the ordinary case for a test, and the loop calls into it every turn.
func TestRunLog_ANilWriterDiscards(t *testing.T) {
	l := newRunLog(nil, "r")
	l.event("agent_start", nil)
	l.usage(aigateway.ChatUsage{})
	l.total(&meter{}, 0, nil)
}

// THE END-TO-END SHAPE OF THE DEFECT: a real run against a supplier that
// reports no usage must not claim zero turns.
func TestRun_ASupplierThatReportsNoUsageIsNotZeroTurns(t *testing.T) {
	cfg := baseConfig(t, silentGateway(t, "done"))
	lg := &bytes.Buffer{}
	cfg.Log = lg

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	total := eventNamed(logLines(t, lg), "agent_usage_total")
	if total == nil {
		t.Fatal("no total line")
	}
	if total["usage_reported"] != false {
		t.Errorf("usage_reported = %v, want false", total["usage_reported"])
	}
	if turns, ok := total["turns"]; ok {
		t.Errorf("turns = %v for an unmetered run; that reads as 'never called a "+
			"model' for work that plainly happened", turns)
	}
}
