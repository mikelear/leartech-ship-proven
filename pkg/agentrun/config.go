package agentrun

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"
	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

// Defaults for the knobs a Job spec usually leaves alone.
const (
	// DefaultRoot is the directory an agent's tools may touch.
	//
	// The clone lands in /workspace in every agent image (WORKDIR /workspace
	// in leartech-agent-base and leartech-agent-go), so a Job that sets
	// nothing still works — and an agent cannot reach the rest of the pod
	// filesystem by accident. The service-account token, the projected
	// gateway key and the git credentials all live outside it.
	DefaultRoot = "/workspace"

	// DefaultMaxTools is the tool-call ceiling for one brief.
	//
	// HIGHER THAN AN INTERACTIVE SHELL'S, because a shell input is one
	// question and a brief is a piece of work: read, edit, build, lint, test,
	// push. It is still a ceiling, and with nobody to raise it a HARD one —
	// which is the point. A runaway agent stops here rather than at the
	// budget, and stopping at a count is cheaper than stopping at a spend.
	DefaultMaxTools = 120
)

// EnvBrief is where a Job can put the brief instead of mounting a file.
const EnvBrief = "LEARTECH_AGENT_BRIEF"

// ErrNoBrief is returned when no brief was supplied.
//
// AN EMPTY BRIEF IS AN ERROR, NOT AN EMPTY RUN. A Job whose brief failed to
// project would otherwise start a model turn with nothing to do, spend a
// little, and exit 0 — reporting success for work nobody asked for.
//
// proven-by: TestResolveBrief_AnEmptyBriefIsRefused
var ErrNoBrief = errors.New("agentrun: no brief")

// ErrNoModel is returned when the environment names no model.
//
// AN AGENT DOES NOT CHOOSE ITS OWN MODEL. The key the controller minted
// carries a model allowlist, and a request outside it is refused by the
// gateway — as an auth-shaped error, which reads as a broken credential
// rather than a missing configuration.
//
// proven-by: TestLoadConfig_RefusesWithNoModel
var ErrNoModel = errors.New("agentrun: no model")

// Config is one run.
//
// EVERY I/O DESTINATION IS A FIELD rather than a package-level default, so a
// test drives the whole thing without a filesystem, a terminal or a network
// — and so the two front doors (this repo's cmd/agent and ship-proven's
// `agent` subcommand) differ only in how they FILL it in.
type Config struct {
	// Brief is the work. Required.
	Brief string

	// Root is what the tools may touch. Empty means [DefaultRoot].
	Root string

	// Client is the gateway. Required — [LoadConfig] builds one from the
	// environment, and a test supplies a fake.
	Client agentloop.Streamer

	// Model is the model to ask. Required.
	Model string

	// RunID and CorrelationKind are for the log lines only; the Client
	// carries its own correlation headers.
	RunID           string
	CorrelationKind string

	// SystemPrompt is what the agent is told. Empty means
	// [DefaultSystemPrompt].
	SystemPrompt string

	// ReadOnly offers only the reading tools: a dry run that can inspect the
	// repository and report, and change nothing.
	ReadOnly bool

	MaxTools    int
	MaxTokens   int
	ExecTimeout time.Duration

	// Timeout gives up after this long. Zero means no limit — the Job's own
	// deadline still applies, and is the real backstop.
	Timeout time.Duration

	// Out is where the agent's answer goes; Log is where the structured
	// record goes. TWO DESTINATIONS ON PURPOSE: a Job that captures stdout
	// gets the answer, and everything about the run stays out of it.
	Out io.Writer
	Log io.Writer

	// Transcript is an optional JSONL record of the loop's own events.
	Transcript io.Writer
}

// LoadConfig fills in everything the environment decides.
//
// THE CREDENTIAL COMES FROM THE ENVIRONMENT AND NOWHERE ELSE.
// [aigateway.FromEnv] also REFUSES a provider-native environment —
// ANTHROPIC_API_KEY and friends — rather than quietly preferring one. An
// agent reaching a provider directly would be unmetered, unattributable and
// outside every budget the controller set, and the failure would look like
// success.
//
// proven-by: TestLoadConfig_TakesTheCredentialFromTheEnvironment
// proven-by: TestLoadConfig_RefusesAProviderNativeEnvironment
// proven-by: TestLoadConfig_RefusesWithNoModel
// proven-by: TestLoadConfig_ReadsTheBriefFromTheEnvironment
func LoadConfig(env func(string) string) (Config, error) {
	client, gw, err := aigateway.FromEnv()
	if err != nil {
		return Config{}, fmt.Errorf("%w — an agent takes its gateway credential "+
			"from the environment. The orchestrator-controller mints a budgeted "+
			"key per AgentRun and projects it as %s, with %s alongside. Nothing "+
			"here reads a session file or mints a key of its own: a Job has no "+
			"browser", err, aigateway.EnvAPIKey, aigateway.EnvURL)
	}
	if gw.Model == "" {
		return Config{}, fmt.Errorf("%w: set %s. The key the controller minted "+
			"carries a model allowlist, and a request outside it is refused by "+
			"the gateway", ErrNoModel, aigateway.EnvModel)
	}
	return Config{
		Brief:           env(EnvBrief),
		Client:          client,
		Model:           gw.Model,
		RunID:           gw.RunID,
		CorrelationKind: correlationKind(gw),
	}, nil
}

// correlationKind names the join key the gateway will file this run's spend
// under, or "none".
//
// SAID OUT LOUD AT THE START. The Client already carries the header —
// aigateway.Config.Client applies it, so nothing here re-decides it — but a
// run whose spend will turn out to be unattributable should say so before it
// starts, rather than leave it to be discovered from an empty Loki query.
//
// proven-by: TestCorrelationKind_NamesTheKeyInForce
// proven-by: TestCorrelationKind_SaysNoneWhenNothingIsSet
func correlationKind(gw aigateway.Config) string {
	switch {
	case gw.RunID != "":
		return aigateway.CorrelateRun
	case gw.SessionID != "":
		return aigateway.CorrelateSession
	default:
		return "none"
	}
}

// DefaultSystemPrompt is what an agent is told when a Plan supplies nothing.
//
// PROVE IT LOCALLY BEFORE PUSHING is the whole reason the language images
// carry a toolchain at all. An agent that pushes and waits for CI to report
// what a local `make lint` would have said in four seconds burns a pipeline
// cycle per mistake, and this estate has paid for that repeatedly.
const DefaultSystemPrompt = "You are working inside a checked-out repository at /workspace.\n" +
	"Read before you write. Make the smallest change that does the job.\n" +
	"This image carries the repository's own toolchain: run its Makefile " +
	"targets — lint, test, build — and fix what they report BEFORE you push. " +
	"A failure you can see locally in seconds costs a pipeline cycle if you " +
	"let CI find it.\n" +
	"When you cannot do what was asked, say so plainly and say why. " +
	"Reporting success for work that did not happen is worse than failing."
