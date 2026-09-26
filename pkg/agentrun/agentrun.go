package agentrun

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"
	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

// Run drives one brief to completion and reports what happened.
//
// THE ORDER IS DELIBERATE: everything local is validated before anything
// costs. A brief that is missing, a root that cannot be resolved or a
// credential that is absent should fail in milliseconds, not after a model
// round trip — and an agent Job that fails for a configuration reason should
// say so rather than spend first.
//
// proven-by: TestRun_RefusesWithoutABrief
// proven-by: TestRun_RefusesWithoutAClient
// proven-by: TestRun_AnswersTheBriefAndReportsUsage
// proven-by: TestRun_ValidatesBeforeAnythingCosts
func Run(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Brief) == "" {
		return fmt.Errorf("%w. Pass one, or set %s. An agent with nothing to do "+
			"would spend a turn finding that out and then exit 0", ErrNoBrief, EnvBrief)
	}
	if cfg.Client == nil {
		return fmt.Errorf("agentrun: no gateway client; call LoadConfig or supply one")
	}
	if cfg.Model == "" {
		return fmt.Errorf("%w", ErrNoModel)
	}

	root := cfg.Root
	if root == "" {
		root = DefaultRoot
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("agentrun: resolving the root: %w", err)
	}

	prompt := cfg.SystemPrompt
	if prompt == "" {
		prompt = DefaultSystemPrompt
	}
	maxTools := cfg.MaxTools
	if maxTools <= 0 {
		maxTools = DefaultMaxTools
	}
	execTimeout := cfg.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = agentloop.DefaultExecTimeout
	}
	out := cfg.Out
	if out == nil {
		out = io.Discard
	}

	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	tools := agentloop.NewRegistry()
	for _, t := range toolsFor(absRoot, cfg.ReadOnly, execTimeout) {
		if err := tools.Add(t); err != nil {
			return fmt.Errorf("agentrun: offering tools: %w", err)
		}
	}
	approver := newFixedApprover(effectsFor(cfg.ReadOnly))
	tools.Govern(newPolicy(cfg.ReadOnly), approver)

	log := newRunLog(cfg.Log, cfg.RunID)
	m := &meter{}
	log.event("agent_start", map[string]any{
		"model":       cfg.Model,
		"root":        absRoot,
		"max_tools":   maxTools,
		"read_only":   cfg.ReadOnly,
		"brief_bytes": len(cfg.Brief),
		"correlation": cfg.CorrelationKind,
		"tools":       tools.Names(),
	})

	var transcript *agentloop.Transcript
	if cfg.Transcript != nil {
		transcript = agentloop.NewTranscript(cfg.Transcript)
	}

	r := &agentloop.Runner{
		Model:      cfg.Model,
		Client:     cfg.Client,
		Tools:      tools,
		Out:        out,
		UI:         agentloop.Quiet{},
		Transcript: transcript,
		MaxTokens:  cfg.MaxTokens,

		// NIL AskContinue IS THE HARD CEILING, and agentloop documents it as
		// "NO, which is what a scripted run needs": nobody is here to answer,
		// so a run that waited would hang until the Job deadline and one that
		// assumed yes would have no ceiling at all.
		AskContinue: nil,

		OnUsage: func(u aigateway.ChatUsage) {
			m.observe(u)
			log.usage(u)
		},
	}

	runErr := r.Run(ctx, agentloop.NewPipeLines(strings.NewReader(cfg.Brief)), prompt, maxTools)

	// REPORTED WHETHER OR NOT IT FAILED. A brief that ran out of tool budget
	// or hit its deadline still spent money, and a cost reported only on
	// success makes the expensive failures the invisible ones.
	//
	// proven-by: TestRun_AFailedRunStillReportsWhatItSpent
	refused, refusals := approver.Refused()
	log.total(m, refused, refusals)

	if runErr != nil {
		log.event("agent_end", map[string]any{"ok": false, "error": runErr.Error()})
		return runErr
	}
	log.event("agent_end", map[string]any{"ok": true})
	return nil
}

// toolsFor is what an unattended run may reach for.
//
// A READ-ONLY RUN IS NOT OFFERED THE WRITING TOOLS AT ALL, rather than being
// offered them and refused at the gate. The gate is the second line of
// defence; a tool that is never advertised cannot be called, and the model is
// not invited to try and then told no.
//
// ROOTED, ALWAYS. Every tool takes the root, so an agent cannot reach the
// rest of the pod filesystem by accident — the clone is the working set, and
// the service-account token, the projected gateway key and the git
// credentials all live outside it.
//
// proven-by: TestToolsFor_AReadOnlyRunOffersNoWriteOrBash
// proven-by: TestToolsFor_EveryToolIsConfinedToTheRoot
func toolsFor(root string, readOnly bool, execTimeout time.Duration) []agentloop.Tool {
	out := []agentloop.Tool{
		agentloop.ReadFile(root),
		agentloop.ListDir(root),
		agentloop.Glob(root),
	}
	if !readOnly {
		out = append(out, agentloop.WriteFile(root), agentloop.Bash(root, execTimeout))
	}
	return out
}
