// Command agent runs one brief unattended.
//
// THIS IS THE BINARY THE PER-LANGUAGE AGENT IMAGES CARRY. It is deliberately
// NOT the whole ship-proven CLI: an agent pod has no business holding `login`,
// browser OAuth or key-minting code, and the credential it uses was already
// minted for it by the orchestrator-controller and projected into the Job.
//
// Everything it does is in
// [github.com/mikelear/leartech-ship-proven/pkg/agentrun]. This file is only
// the argv-to-Config translation, kept thin on purpose — logic here would be
// logic the interactive front door in leartech-ba-service could not reach,
// and the two would drift.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"

	"github.com/mikelear/leartech-ship-proven/pkg/agentrun"
)

// version is stamped at build time: -ldflags="-X main.version=..."
var version = "dev"

// Exit codes, so a Job's status distinguishes the cases a controller cares
// about. NOT just 0 and 1: "you configured me wrong" and "the work failed"
// lead to different remedies, and collapsing them makes the wrong one look
// like the right one.
const (
	exitOK       = 0
	exitFailed   = 1
	exitBadUsage = 2
	exitNoConfig = 3
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(argv []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	brief := fs.String("brief", "", "file holding the brief; - for stdin. Defaults to $"+agentrun.EnvBrief)
	root := fs.String("root", agentrun.DefaultRoot, "directory the agent's tools may touch")
	maxTools := fs.Int("max-tools", agentrun.DefaultMaxTools, "hard ceiling on tool calls; there is nobody to raise it")
	maxTokens := fs.Int("max-tokens", 0, "cap each reply (0 = the model's default)")
	systemFile := fs.String("system-file", "", "file holding the system prompt (default: built in)")
	transcript := fs.String("transcript", "", "write a JSONL record of the loop's events")
	timeout := fs.Duration("timeout", 0, "give up after this long (0 = no limit; the Job's deadline still applies)")
	execTimeout := fs.Duration("exec-timeout", agentloop.DefaultExecTimeout, "how long one script may run")
	readOnly := fs.Bool("read-only", false,
		"offer only the reading tools. A dry run: inspect the repository and "+
			"report, and change nothing")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(argv); err != nil {
		return exitBadUsage
	}
	if *showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return exitOK
	}

	// EVERYTHING LOCAL FIRST, BEFORE ANYTHING COSTS. A typo in --brief or an
	// unwritable transcript should fail in milliseconds rather than after a
	// model round trip.
	briefText, err := agentrun.ResolveBrief(*brief, os.Stdin, os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitBadUsage
	}

	cfg, err := agentrun.LoadConfig(os.Getenv)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		// A MISSING CREDENTIAL IS NOT A FAILED BRIEF. The controller reads the
		// exit code, and "my Secret did not project" wants a different remedy
		// from "the work did not succeed".
		return exitNoConfig
	}

	cfg.Brief = briefText
	cfg.Root = *root
	cfg.MaxTools = *maxTools
	cfg.MaxTokens = *maxTokens
	cfg.ReadOnly = *readOnly
	cfg.ExecTimeout = *execTimeout
	cfg.Timeout = *timeout
	cfg.Out = stdout
	cfg.Log = stderr

	if *systemFile != "" {
		b, err := os.ReadFile(*systemFile)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "reading the system prompt: %v\n", err)
			return exitBadUsage
		}
		cfg.SystemPrompt = string(b)
	}
	if *transcript != "" {
		f, err := os.OpenFile(*transcript, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "opening the transcript: %v\n", err)
			return exitBadUsage
		}
		defer func() { _ = f.Close() }()
		cfg.Transcript = f
	}

	// SIGTERM IS THE CONTROLLER ENDING THE RUN, and it must reach the loop so
	// a turn in flight is abandoned and the totals are still written. Without
	// this the pod is killed mid-turn and the run's own account of what it
	// spent is lost — which is the exact gap the recorder reads the gateway's
	// log to cover.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := agentrun.Run(ctx, cfg); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		if errors.Is(err, agentrun.ErrNoBrief) || errors.Is(err, agentrun.ErrNoModel) {
			return exitBadUsage
		}
		return exitFailed
	}
	return exitOK
}
