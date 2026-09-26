package agentrun

import (
	"strings"
	"testing"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"
)

func req(tool string, e agentloop.Effect) agentloop.Request {
	return agentloop.Request{Tool: tool, Effect: e}
}

// WRITES AND EXECUTES REACH Ask AND NEVER Allow. If they reached Allow the
// gate would stop being consulted, and a later change to what counts as a
// write would go unnoticed.
func TestNewPolicy_WritesAndExecutesReachAskNotAllow(t *testing.T) {
	p := newPolicy(false)
	for _, e := range []agentloop.Effect{agentloop.Writes, agentloop.Executes} {
		v, reason := p.Decide(req("local__write_file", e))
		if v == agentloop.Allow {
			t.Errorf("%s reached Allow; the gate is no longer consulted for it", e)
		}
		if v != agentloop.Ask {
			t.Errorf("%s = %v, want Ask", e, v)
		}
		if reason == "" {
			t.Errorf("%s has no reason; the reason is what a decision is recorded "+
				"against, and a missing one is worse than a wrong one", e)
		}
	}
}

// A READ-ONLY RUN DENIES, so a tool offered by mistake is still refused —
// two independent reasons, neither relying on the other being right.
func TestNewPolicy_AReadOnlyRunDeniesWritesAndExecutes(t *testing.T) {
	p := newPolicy(true)
	for _, e := range []agentloop.Effect{agentloop.Writes, agentloop.Executes} {
		v, reason := p.Decide(req("local__write_file", e))
		if v != agentloop.Deny {
			t.Errorf("%s = %v in a read-only run, want Deny", e, v)
		}
		if !strings.Contains(reason, "read-only") {
			t.Errorf("%s reason = %q, want it to say why", e, reason)
		}
	}
}

// READS ARE ALWAYS ALLOWED, in both modes. A gate that asked before every
// file read would be clicked through, and an unattended run would stop.
func TestNewPolicy_ReadsAreAlwaysAllowed(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		v, _ := newPolicy(readOnly).Decide(req("local__read_file", agentloop.Reads))
		if v != agentloop.Allow {
			t.Errorf("reads = %v with readOnly=%v, want Allow", v, readOnly)
		}
	}
}

// A REMOTE TOOL DOES NOT CHANGE A FILE ON THIS MACHINE. One string per
// effect class told every MCP tool it was editing local files.
func TestReasonForWrites_DistinguishesLocalFromRemote(t *testing.T) {
	local := reasonForWrites(req("local__write_file", agentloop.Writes))
	if !strings.Contains(local, "this machine") {
		t.Errorf("local reason = %q", local)
	}
	// THE REAL NAME CONVENTION IS mcp__<server>__<tool>. My first version of
	// this test used "pr_context__open_pr", ServerOf correctly returned "",
	// and the test failed against working code — a fixture that cannot
	// express the case it is about would have passed a broken one just as
	// readily.
	remote := reasonForWrites(req("mcp__pr_context__open_pr", agentloop.Writes))
	if strings.Contains(remote, "this machine") {
		t.Errorf("remote reason = %q; it does not change a file here, and saying "+
			"so where someone decides is worse than saying nothing", remote)
	}
	if !strings.Contains(remote, "pr_context") {
		t.Errorf("remote reason = %q, want the server named", remote)
	}
}

func TestReasonForExecutes_NamesTheServersOwnClaim(t *testing.T) {
	remote := reasonForExecutes(req("mcp__pr_context__merge", agentloop.Executes))
	if !strings.Contains(remote, "pr_context") || !strings.Contains(remote, "destructive") {
		t.Errorf("reason = %q, want the server's own claim", remote)
	}
	local := reasonForExecutes(req("local__bash", agentloop.Executes))
	if !strings.Contains(local, "this machine") {
		t.Errorf("local reason = %q", local)
	}
}

// THE APPROVER PERMITS EXACTLY WHAT IT WAS SEEDED WITH. Anything else is
// refused rather than invented — there is nobody to ask.
func TestFixedApprover_ApprovesOnlyTheSeededEffects(t *testing.T) {
	a := newFixedApprover(map[agentloop.Effect]bool{agentloop.Writes: true})

	if !a.Approve(req("local__write_file", agentloop.Writes), "r") {
		t.Error("writes was refused despite being seeded")
	}
	if a.Approve(req("local__bash", agentloop.Executes), "r") {
		t.Error("executes was approved having only seeded writes")
	}
	// A false value in the map is not an approval.
	b := newFixedApprover(map[agentloop.Effect]bool{agentloop.Writes: false})
	if b.Approve(req("local__write_file", agentloop.Writes), "r") {
		t.Error("a false entry was treated as an approval")
	}
}

// REFUSALS ARE COUNTED. A run declined forty tool calls and then reported it
// could not finish looks like a model problem; the count identifies it as a
// permission one.
func TestFixedApprover_CountsWhatItRefused(t *testing.T) {
	a := newFixedApprover(map[agentloop.Effect]bool{})
	a.Approve(req("local__write_file", agentloop.Writes), "r")
	a.Approve(req("local__bash", agentloop.Executes), "r")
	a.Approve(req("local__bash", agentloop.Executes), "r")

	n, names := a.Refused()
	if n != 3 {
		t.Errorf("refused = %d, want 3", n)
	}
	if len(names) != 3 || names[0] != "local__write_file" {
		t.Errorf("names = %v, want the tools that were refused", names)
	}
}

// THE NAME LIST IS BOUNDED, so a run that is refused thousands of calls
// produces a summary rather than a second transcript.
func TestFixedApprover_TheRefusalListIsBounded(t *testing.T) {
	a := newFixedApprover(map[agentloop.Effect]bool{})
	for range 100 {
		a.Approve(req("local__bash", agentloop.Executes), "r")
	}
	n, names := a.Refused()
	if n != 100 {
		t.Errorf("count = %d, want all 100 counted", n)
	}
	if len(names) > 16 {
		t.Errorf("names = %d entries; the list must stay a summary", len(names))
	}
}

func TestFixedApprover_AReadOnlyRunApprovesNothing(t *testing.T) {
	a := newFixedApprover(effectsFor(true))
	for _, e := range []agentloop.Effect{agentloop.Writes, agentloop.Executes} {
		if a.Approve(req("x", e), "r") {
			t.Errorf("%s was approved in a read-only run", e)
		}
	}
}

func TestEffectsFor_AWritingRunMayWriteAndExecute(t *testing.T) {
	got := effectsFor(false)
	if !got[agentloop.Writes] || !got[agentloop.Executes] {
		t.Errorf("effects = %v; writing code and running make IS the brief, and "+
			"an agent that had to ask would deadlock on its first edit", got)
	}
}

func TestEffectsFor_AReadOnlyRunMayDoNeither(t *testing.T) {
	if got := effectsFor(true); len(got) != 0 {
		t.Errorf("effects = %v, want empty", got)
	}
}
