package agentrun

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

// runLog writes the estate's structured log lines for one run.
//
// JSON, ONE OBJECT PER LINE, KEYED ON run_id. That is what Loki's `| json`
// parser needs, and run_id is the join key every other tool in the estate
// correlates on — the gateway's usage rows, lokiserver's run_report and the
// recorder all key on it, so a line without one is a line nothing can
// attribute to the work it describes.
//
// proven-by: TestRunLog_EveryLineCarriesTheRunID
// proven-by: TestRunLog_WritesOneJSONObjectPerLine
type runLog struct {
	out   io.Writer
	runID string
	now   func() time.Time
}

func newRunLog(out io.Writer, runID string) *runLog {
	if out == nil {
		out = io.Discard
	}
	return &runLog{out: out, runID: runID, now: time.Now}
}

func (l *runLog) event(name string, fields map[string]any) {
	rec := map[string]any{
		"event":  name,
		"run_id": l.runID,
		"ts":     l.now().UTC().Format(time.RFC3339),
	}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		// A LOG LINE THAT CANNOT BE ENCODED MUST NOT TAKE THE RUN OUT, and
		// must not vanish either: a run lost to its own instrumentation is
		// the worst of both. The fallback keeps the event name and the run
		// id, which is what makes the gap attributable.
		//
		// proven-by: TestRunLog_AnUnencodableFieldDoesNotStopTheRun
		_, _ = fmt.Fprintf(l.out, `{"event":%q,"run_id":%q,"log_error":%q}`+"\n",
			name, l.runID, err.Error())
		return
	}
	// A failed write is not worth failing a run over, and there is nowhere
	// better to report it than the writer that just failed.
	_, _ = fmt.Fprintf(l.out, "%s\n", b)
}

// usage records one turn's metered cost.
//
// THE CONVENTION IS NAMED ON THE LINE. The gateway's own usage log is
// ADDITIVE — its prompt_tokens is the uncached remainder — and this response
// is SUBSET, with cache reads already inside it. Mixing the two yields a
// cache hit rate over 100%, and a reader reconciling the two observers has no
// way to tell which they are holding unless the line says.
//
// proven-by: TestRunLog_NamesTheTokenConvention
// proven-by: TestRunLog_ASupplierWithNoCacheReportsNoCacheFields
func (l *runLog) usage(u aigateway.ChatUsage) {
	f := map[string]any{
		"prompt_tokens":            u.PromptTokens,
		"completion_tokens":        u.CompletionTokens,
		"prompt_tokens_convention": "subset",
	}
	// ABSENT IS NOT ZERO. A supplier with no prompt cache reports nothing
	// about one, and writing 0 there would call that a total cache miss —
	// inventing a denominator the supplier never offered.
	if u.CacheReported() {
		f["cache_read_tokens"] = u.CachedTokens()
		f["cache_write_tokens"] = u.CacheWrite5mTokens() + u.CacheWrite1hTokens()
	}
	l.event("turn_usage", f)
}

// meter accumulates the run's own account of what it spent.
//
// BESIDE THE GATEWAY'S, NEVER INSTEAD OF IT. The gateway meters every call
// independently and its figures survive a run that dies before reporting —
// which is exactly the run worth measuring. This is the agent's self-report,
// and the recorder compares the two rather than trusting either.
type meter struct {
	turns, prompt, completion int
	cacheRead, cacheWrite     int

	// reported counts turns where the supplier said anything about caching
	// at all, and it is the hit-rate DENOMINATOR. Turns from a supplier with
	// no cache are excluded rather than counted as misses.
	reported int
}

func (m *meter) observe(u aigateway.ChatUsage) {
	m.turns++
	m.prompt += u.PromptTokens
	m.completion += u.CompletionTokens
	if u.CacheReported() {
		m.reported++
		m.cacheRead += u.CachedTokens()
		m.cacheWrite += u.CacheWrite5mTokens() + u.CacheWrite1hTokens()
	}
}

// total writes the run's own account.
//
// A SUPPLIER THAT REPORTS NO USAGE IS NOT A RUN WITH NO TURNS. The turn count
// here comes from the usage callback, so a supplier with no metering left it
// at 0 — which reads as "this run never called a model" for work that
// demonstrably happened. Found by a fake that faithfully omitted usage.
//
// NO CONFIRMED INSTANCE. This comment said the gateway's free echo model was
// one; a real run through the published agent image on 2026-09-27 showed
// echo reporting prompt_tokens 1 / completion_tokens 1 like any other
// supplier. The shape is still worth guarding — nothing in the protocol
// requires a usage block — but naming a false example was worse than naming
// none.
//
// The numbers are therefore OMITTED rather than written as zero, and
// usage_reported says which case a reader is looking at. It is the same
// absent-is-not-zero discipline the recorder applies to the other direction.
//
// proven-by: TestRunLog_TheTotalOmitsFiguresWhenNothingWasMetered
// proven-by: TestRun_ASupplierThatReportsNoUsageIsNotZeroTurns
func (l *runLog) total(m *meter, refused int, refusals []string) {
	f := map[string]any{"usage_reported": m.turns > 0}
	if m.turns > 0 {
		f["turns"] = m.turns
		f["prompt_tokens"] = m.prompt
		f["completion_tokens"] = m.completion
		f["cache_read_tokens"] = m.cacheRead
		f["cache_write_tokens"] = m.cacheWrite
		f["cache_reported"] = m.reported
	} else {
		f["note"] = "the supplier reported no usage for this run; the figures are " +
			"absent rather than zero. The gateway meters every call " +
			"independently — read its usage log for run_id " + l.runID
	}
	// REFUSALS ARE PART OF THE ACCOUNT. A run that was declined forty tool
	// calls and then said it could not finish looks like a model problem; the
	// count is what identifies it as a permission one.
	//
	// proven-by: TestRunLog_TheTotalCarriesRefusals
	if refused > 0 {
		f["tool_calls_refused"] = refused
		f["tools_refused"] = refusals
	}
	l.event("agent_usage_total", f)
}
