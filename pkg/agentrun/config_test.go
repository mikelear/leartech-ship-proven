package agentrun

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikelear/leartech-go-common/pkg/aigateway"
)

// THE CREDENTIAL COMES FROM THE ENVIRONMENT, which is the whole reason this
// package exists separately from an interactive client: a Job has no session
// file and no browser.
func TestLoadConfig_TakesTheCredentialFromTheEnvironment(t *testing.T) {
	gw, _ := fakeGateway(t, "ok")
	controllerEnv(t, gw)

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Client == nil {
		t.Error("no client built from a complete environment")
	}
	if cfg.Model != "echo" {
		t.Errorf("model = %q, want echo", cfg.Model)
	}
	if cfg.RunID != "agentrun-abc123" {
		t.Errorf("run id = %q; without it the gateway's usage rows cannot be "+
			"tied to this run and run_report finds nothing", cfg.RunID)
	}
	if cfg.CorrelationKind != aigateway.CorrelateRun {
		t.Errorf("correlation = %q, want run", cfg.CorrelationKind)
	}
}

// A MISSING CREDENTIAL IS A REFUSAL THAT NAMES THE CONTRACT. A Job whose
// Secret failed to project would otherwise reach a model call and fail there,
// which reads as a gateway fault rather than a projection one.
func TestLoadConfig_RefusesWithNoCredential(t *testing.T) {
	gw, _ := fakeGateway(t, "ok")
	controllerEnv(t, gw)
	t.Setenv(aigateway.EnvAPIKey, "")

	_, err := LoadConfig(os.Getenv)
	if err == nil {
		t.Fatal("an agent with no gateway credential must not start")
	}
	if !strings.Contains(err.Error(), aigateway.EnvAPIKey) {
		t.Errorf("the refusal must name the variable the controller projects: %v", err)
	}
}

// PROVIDER-NATIVE ENVIRONMENT IS REFUSED, NOT PREFERRED. An agent reaching
// Anthropic directly would be unmetered, unattributable and outside every
// budget the controller set — and it would look like success.
func TestLoadConfig_RefusesAProviderNativeEnvironment(t *testing.T) {
	gw, _ := fakeGateway(t, "ok")
	controllerEnv(t, gw)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-not-be-used")

	if _, err := LoadConfig(os.Getenv); err == nil {
		t.Fatal("a provider-native key must stop the run: spend through it is " +
			"invisible to the gateway, so the per-run budget bounds nothing")
	}
}

// A MODEL IS REQUIRED, and the reason is the key rather than the code: the
// controller stamps a model allowlist on the key it mints, and a request
// outside it is refused by the gateway with an auth-shaped error.
func TestLoadConfig_RefusesWithNoModel(t *testing.T) {
	gw, _ := fakeGateway(t, "ok")
	controllerEnv(t, gw)
	t.Setenv(aigateway.EnvModel, "")

	_, err := LoadConfig(os.Getenv)
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("err = %v, want ErrNoModel", err)
	}
	if !strings.Contains(err.Error(), aigateway.EnvModel) {
		t.Errorf("the refusal must name the variable to set: %v", err)
	}
}

// The brief may arrive in the environment, so a Job spec need not mount a
// file to carry one.
func TestLoadConfig_ReadsTheBriefFromTheEnvironment(t *testing.T) {
	gw, _ := fakeGateway(t, "ok")
	controllerEnv(t, gw)
	t.Setenv(EnvBrief, "raise the coverage floor")

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Brief != "raise the coverage floor" {
		t.Errorf("brief = %q", cfg.Brief)
	}
}

func TestCorrelationKind_NamesTheKeyInForce(t *testing.T) {
	if got := correlationKind(aigateway.Config{RunID: "r1"}); got != aigateway.CorrelateRun {
		t.Errorf("kind = %q, want run", got)
	}
	if got := correlationKind(aigateway.Config{SessionID: "s1"}); got != aigateway.CorrelateSession {
		t.Errorf("kind = %q, want session", got)
	}
	// A run id wins: an agent is a run, and sending both would leave the
	// gateway to decide which is authoritative.
	both := aigateway.Config{RunID: "r1", SessionID: "s1"}
	if got := correlationKind(both); got != aigateway.CorrelateRun {
		t.Errorf("kind = %q, want run to win", got)
	}
}

func TestCorrelationKind_SaysNoneWhenNothingIsSet(t *testing.T) {
	if got := correlationKind(aigateway.Config{}); got != "none" {
		t.Errorf("kind = %q, want none — an unattributable run should say so at "+
			"the start, not be discovered from an empty Loki query", got)
	}
}

func TestResolveBrief_PrefersThePathOverTheEnvironment(t *testing.T) {
	f := filepath.Join(t.TempDir(), "brief.txt")
	if err := os.WriteFile(f, []byte("from the file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveBrief(f, strings.NewReader(""), func(string) string { return "from the env" })
	if err != nil {
		t.Fatal(err)
	}
	if got != "from the file" {
		t.Errorf("brief = %q; an explicit path must win, or a Job runs an "+
			"inherited brief instead of the one it was given", got)
	}
}

func TestResolveBrief_ReadsStdinForADash(t *testing.T) {
	got, err := ResolveBrief("-", strings.NewReader("piped in"), func(string) string { return "ignored" })
	if err != nil {
		t.Fatal(err)
	}
	if got != "piped in" {
		t.Errorf("brief = %q, want the piped text", got)
	}
}

func TestResolveBrief_FallsBackToTheEnvironment(t *testing.T) {
	got, err := ResolveBrief("", strings.NewReader(""), func(k string) string {
		if k == EnvBrief {
			return "from the env"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "from the env" {
		t.Errorf("brief = %q", got)
	}
}

func TestResolveBrief_AnEmptyBriefIsRefused(t *testing.T) {
	for name, text := range map[string]string{"empty": "", "whitespace": "  \n\t "} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveBrief("", strings.NewReader(""), func(string) string { return text })
			if !errors.Is(err, ErrNoBrief) {
				t.Errorf("err = %v, want ErrNoBrief — an agent with nothing to do "+
					"would spend a turn finding out and exit 0", err)
			}
		})
	}
}

// A MISSING FILE SAYS WHICH FILE. "no brief" for a path that was supplied and
// unreadable sends a reader looking in the wrong place.
func TestResolveBrief_AMissingFileSaysWhichFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ResolveBrief(missing, strings.NewReader(""), func(string) string { return "" })
	if err == nil {
		t.Fatal("a missing brief file must fail")
	}
	if !strings.Contains(err.Error(), "nope.txt") {
		t.Errorf("err = %v, want the path named", err)
	}
	if errors.Is(err, ErrNoBrief) {
		t.Error("an unreadable path reported as 'no brief' sends a reader to the " +
			"environment instead of to the typo")
	}
}
