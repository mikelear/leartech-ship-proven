package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikelear/leartech-go-common/pkg/agentloop"
)

// baseConfig is a run that works, so each test changes one thing.
func baseConfig(t *testing.T, gateway string) Config {
	t.Helper()
	controllerEnv(t, gateway)
	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Brief = "summarise what is here"
	cfg.Root = t.TempDir()
	cfg.ReadOnly = true
	cfg.Out = &bytes.Buffer{}
	cfg.Log = &bytes.Buffer{}
	return cfg
}

// The ordinary run: the brief reaches the model, the answer reaches Out, and
// the cost is recorded.
func TestRun_AnswersTheBriefAndReportsUsage(t *testing.T) {
	gw, seen := fakeGateway(t, "there are three files")
	cfg := baseConfig(t, gw)
	out, lg := &bytes.Buffer{}, &bytes.Buffer{}
	cfg.Out, cfg.Log = out, lg

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "three files") {
		t.Errorf("the answer did not reach Out: %q", out.String())
	}
	if len(seen()) == 0 {
		t.Fatal("the gateway was never called")
	}
	// The run id travelled, or the spend is unattributable.
	if got := seen()[0].Header.Get("X-Leartech-Run-Id"); got != "agentrun-abc123" {
		t.Errorf("X-Leartech-Run-Id = %q; without it the gateway's usage rows "+
			"cannot be tied to this run", got)
	}

	recs := logLines(t, lg)
	for _, want := range []string{"agent_start", "turn_usage", "agent_usage_total", "agent_end"} {
		if eventNamed(recs, want) == nil {
			t.Errorf("no %s line:\n%s", want, lg.String())
		}
	}
	if end := eventNamed(recs, "agent_end"); end["ok"] != true {
		t.Errorf("agent_end = %v, want ok", end)
	}
}

func TestRun_RefusesWithoutABrief(t *testing.T) {
	gw, seen := fakeGateway(t, "x")
	cfg := baseConfig(t, gw)
	cfg.Brief = "   "

	err := Run(context.Background(), cfg)
	if !errors.Is(err, ErrNoBrief) {
		t.Fatalf("err = %v, want ErrNoBrief", err)
	}
	if n := len(seen()); n != 0 {
		t.Errorf("the gateway was called %d time(s) for an empty brief", n)
	}
}

func TestRun_RefusesWithoutAClient(t *testing.T) {
	gw, _ := fakeGateway(t, "x")
	cfg := baseConfig(t, gw)
	cfg.Client = nil

	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("a run with no client must not start")
	}
}

// LOCAL VALIDATION BEFORE ANYTHING COSTS. Every refusal below should happen
// without a single request reaching the gateway.
func TestRun_ValidatesBeforeAnythingCosts(t *testing.T) {
	gw, seen := fakeGateway(t, "x")

	for name, mutate := range map[string]func(*Config){
		"no brief": func(c *Config) { c.Brief = "" },
		"no model": func(c *Config) { c.Model = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := baseConfig(t, gw)
			before := len(seen())
			mutate(&cfg)
			if err := Run(context.Background(), cfg); err == nil {
				t.Fatal("want a refusal")
			}
			if len(seen()) != before {
				t.Errorf("%s reached the gateway before being refused", name)
			}
		})
	}
}

// THE TOTAL IS REPORTED WHETHER OR NOT THE RUN SUCCEEDED. A brief that hit
// its ceiling still spent money, and a cost reported only on success makes
// the expensive failures the invisible ones.
func TestRun_AFailedRunStillReportsWhatItSpent(t *testing.T) {
	cfg := baseConfig(t, deadGateway(t))
	lg := &bytes.Buffer{}
	cfg.Log = lg

	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("a truncated stream should fail the run")
	}
	recs := logLines(t, lg)
	if eventNamed(recs, "agent_usage_total") == nil {
		t.Errorf("no total on a failed run:\n%s", lg.String())
	}
	end := eventNamed(recs, "agent_end")
	if end == nil || end["ok"] != false {
		t.Errorf("agent_end = %v, want ok=false", end)
	}
	if end["error"] == nil || end["error"] == "" {
		t.Error("the end line records no reason")
	}
}

// A READ-ONLY RUN IS NOT OFFERED THE WRITING TOOLS AT ALL. The gate is the
// second line of defence; a tool never advertised cannot be called.
func TestToolsFor_AReadOnlyRunOffersNoWriteOrBash(t *testing.T) {
	ro := names(t, toolsFor(t.TempDir(), true, agentloop.DefaultExecTimeout))
	for _, banned := range []string{"local__write_file", "local__bash"} {
		if strings.Contains(ro, banned) {
			t.Errorf("%q is offered in a read-only run; tools = %s", banned, ro)
		}
	}
	// And a writing run DOES offer them, or the check above passes against a
	// registry that offers nothing at all.
	rw := names(t, toolsFor(t.TempDir(), false, agentloop.DefaultExecTimeout))
	for _, want := range []string{"local__write_file", "local__bash"} {
		if !strings.Contains(rw, want) {
			t.Errorf("%q is missing from a writing run; tools = %s", want, rw)
		}
	}
}

func names(t *testing.T, tools []agentloop.Tool) string {
	t.Helper()
	reg := agentloop.NewRegistry()
	for _, tl := range tools {
		if err := reg.Add(tl); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(reg.Names(), ",")
}

// EVERY TOOL IS CONFINED TO THE ROOT, so a brief cannot reach the
// service-account token, the projected gateway key or the git credentials.
// The clone is the working set.
//
// ASSERTED THROUGH THE TOOLS Run ACTUALLY BUILDS: the confinement is only
// worth anything if toolsFor passes the root to all of them, and passing ""
// to one would be invisible to a test that constructed its own.
func TestToolsFor_EveryToolIsConfinedToTheRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("sk-lt-the-controllers-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg := agentloop.NewRegistry()
	for _, tl := range toolsFor(root, false, agentloop.DefaultExecTimeout) {
		if err := reg.Add(tl); err != nil {
			t.Fatal(err)
		}
	}
	reg.Govern(newPolicy(false), newFixedApprover(effectsFor(false)))

	// THE POSITIVE CONTROL FIRST. Without it, a registry that refuses
	// everything would satisfy every assertion below while proving nothing
	// about the root.
	got, ok := reg.Run("local__read_file", []byte(`{"path":"inside.txt"}`))
	if !ok || !strings.Contains(got, "ok") {
		t.Fatalf("reading INSIDE the root failed: ok=%v %q", ok, got)
	}

	// Asserted on CONTENT, not on the ok flag: Registry.Run reports false for
	// a refusal AND for any tool error, so a flag-keyed test cannot tell
	// "confined" from "misspelled the tool name".
	got, _ = reg.Run("local__read_file", []byte(`{"path":`+quote(outside)+`}`))
	if strings.Contains(got, "sk-lt-the-controllers-key") {
		t.Errorf("the reading tool returned a file outside the root:\n%s", got)
	}

	target := filepath.Join(filepath.Dir(outside), "written.txt")
	_, _ = reg.Run("local__write_file", []byte(`{"path":`+quote(target)+`,"content":"x"}`))
	if _, err := os.Stat(target); err == nil {
		t.Errorf("the writing tool created %s, outside the root", target)
	}
	// The same write INSIDE the root lands, so the check above is about the
	// root rather than about writing being broken.
	if res, ok := reg.Run("local__write_file", []byte(`{"path":"written.txt","content":"x"}`)); !ok {
		t.Fatalf("writing inside the root failed (%q)", res)
	}
	if _, err := os.Stat(filepath.Join(root, "written.txt")); err != nil {
		t.Fatalf("the in-root write did not land: %v", err)
	}
}

func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
