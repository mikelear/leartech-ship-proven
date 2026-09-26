package agentrun

import (
	"sync"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"
)

// The gate, and who answers it when nobody is there.
//
// THE POLICY IS THE SAME SHAPE AN INTERACTIVE SHELL USES: reads are allowed,
// writes and executes reach Ask and never Allow. What differs is the ANSWER,
// not the decision — an unattended run pre-seeds the effects it is willing to
// permit and refuses everything else.
//
// PRE-SEEDING RATHER THAN WIDENING THE POLICY is the distinction that makes
// this safe to reason about. If writes reached Allow, the gate would stop
// being consulted and a later change to what counts as a write would not be
// noticed. Because they reach Ask, every call is still decided, and the
// decision is recorded in one place.

// newPolicy builds the gate for a run.
//
// A READ-ONLY RUN DENIES rather than merely not-approving, so a tool offered
// by mistake is still refused, with a reason that says which flag would have
// permitted it.
//
// proven-by: TestNewPolicy_WritesAndExecutesReachAskNotAllow
// proven-by: TestNewPolicy_AReadOnlyRunDeniesWritesAndExecutes
// proven-by: TestNewPolicy_ReadsAreAlwaysAllowed
func newPolicy(readOnly bool) *agentloop.Policy {
	p := agentloop.NewPolicy(agentloop.ForEffect(agentloop.Reads, agentloop.Allow, ""))
	if readOnly {
		p.Add(agentloop.ForEffectReason(agentloop.Writes, agentloop.Deny, deniedBecauseReadOnly))
		p.Add(agentloop.ForEffectReason(agentloop.Executes, agentloop.Deny, deniedBecauseReadOnly))
		return p
	}
	p.Add(agentloop.ForEffectReason(agentloop.Writes, agentloop.Ask, reasonForWrites))
	p.Add(agentloop.ForEffectReason(agentloop.Executes, agentloop.Ask, reasonForExecutes))
	return p
}

func deniedBecauseReadOnly(agentloop.Request) string {
	return "this run is read-only: it may inspect the repository and report, " +
		"and change nothing"
}

// reasonForWrites says what will actually happen.
//
// A REMOTE TOOL DOES NOT CHANGE A FILE ON THIS MACHINE. The reason is
// recorded on the decision, so a wrong one is worse than none — and a single
// string per effect class told every MCP tool it was editing local files.
//
// proven-by: TestReasonForWrites_DistinguishesLocalFromRemote
func reasonForWrites(req agentloop.Request) string {
	if s := agentloop.ServerOf(req.Tool); s != "" {
		return "it runs on " + s + ", which has not declared it read-only"
	}
	return "it changes a file on this machine"
}

// reasonForExecutes says why a tool reached the strictest class.
//
// proven-by: TestReasonForExecutes_NamesTheServersOwnClaim
func reasonForExecutes(req agentloop.Request) string {
	if s := agentloop.ServerOf(req.Tool); s != "" {
		return s + " declares this tool destructive"
	}
	return "it runs a script on this machine"
}

// fixedApprover permits exactly the effects it was given, and nothing else.
//
// NO MODES, BECAUSE NOBODY CAN CHANGE ONE MID-RUN. The interactive shell has
// an approver whose set moves as the operator switches between ask and auto;
// carrying that machinery here would be code that cannot be reached and a
// mutable gate in a process with no operator. The set is decided once, before
// the first turn, and a call outside it is refused rather than invented.
//
// THE REFUSAL IS COUNTED. A run that quietly declined forty tool calls and
// then reported that it could not finish the work looks like a model problem;
// the count is what says it was a permission problem.
//
// proven-by: TestFixedApprover_ApprovesOnlyTheSeededEffects
// proven-by: TestFixedApprover_CountsWhatItRefused
// proven-by: TestFixedApprover_AReadOnlyRunApprovesNothing
type fixedApprover struct {
	mu       sync.Mutex
	effects  map[agentloop.Effect]bool
	refused  int
	refusals []string
}

func newFixedApprover(effects map[agentloop.Effect]bool) *fixedApprover {
	set := make(map[agentloop.Effect]bool, len(effects))
	for e, ok := range effects {
		if ok {
			set[e] = true
		}
	}
	return &fixedApprover{effects: set}
}

func (f *fixedApprover) Approve(req agentloop.Request, _ string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.effects[req.Effect] {
		return true
	}
	f.refused++
	// Bounded: the names are for a summary line, not a second transcript.
	if len(f.refusals) < 16 {
		f.refusals = append(f.refusals, req.Tool)
	}
	return false
}

// Refused reports how many calls the gate turned down, and which tools.
func (f *fixedApprover) Refused() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refused, append([]string(nil), f.refusals...)
}

// effectsFor is what a run of this kind may do.
//
// WRITES AND EXECUTES ARE THE BRIEF. An agent asked to fix a lint failure has
// to edit a file and run `make lint`; a gate it could not satisfy would
// deadlock on the first edit. A read-only run seeds nothing, so its gate
// refuses both even though the policy above already denies them — two
// independent reasons, and neither relies on the other being right.
//
// proven-by: TestEffectsFor_AWritingRunMayWriteAndExecute
// proven-by: TestEffectsFor_AReadOnlyRunMayDoNeither
func effectsFor(readOnly bool) map[agentloop.Effect]bool {
	if readOnly {
		return map[agentloop.Effect]bool{}
	}
	return map[agentloop.Effect]bool{
		agentloop.Writes:   true,
		agentloop.Executes: true,
	}
}
