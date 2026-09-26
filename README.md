# leartech-ship-proven

The agent that runs one brief with nobody at the keyboard.

```
agent --brief work.md --root /workspace
LEARTECH_AGENT_BRIEF="fix the lint failure" agent
```

It takes its gateway credential from the environment, because the
orchestrator-controller mints a budgeted key per `AgentRun` and projects it
into the Job:

| variable | |
|---|---|
| `LEARTECH_AIGW_URL` | the gateway |
| `LEARTECH_AIGW_API_KEY` | the key the controller minted for this run |
| `LEARTECH_AIGW_MODEL` | the model, which the key's allowlist must permit |
| `LEARTECH_RUN_ID` | the join key every other tool correlates on |
| `LEARTECH_AGENT_BRIEF` | the brief, if not passed as a file |

Nothing here reads a session file, opens a browser or mints a key: a Job has
none of those. A provider-native environment (`ANTHROPIC_API_KEY`) is
**refused** rather than preferred — spend through it would be unmetered,
unattributable and outside the budget the controller set.

## Layout

- **`pkg/agentrun`** — everything an unattended run needs around the loop:
  the credential, the brief, the gate's pre-seeded answers, the hard tool
  ceiling, and the JSON record keyed on `run_id`.
- **`cmd/agent`** — the binary the agent images carry. Flags to `Config`,
  and nothing else.

The loop itself — turns, tools, the gate — is
[`leartech-go-common/pkg/agentloop`](https://github.com/mikelear/leartech-go-common),
shared with `ship-proven shell`, so an agent run reproduces interactively.

## Output

The answer goes to **stdout**; everything about the run goes to **stderr** as
one JSON object per line, so a Job capturing stdout gets the answer and
nothing else.

```json
{"event":"agent_start","run_id":"agentrun-abc","model":"echo","read_only":false,...}
{"event":"turn_usage","run_id":"agentrun-abc","prompt_tokens":11,"prompt_tokens_convention":"subset"}
{"event":"agent_usage_total","run_id":"agentrun-abc","usage_reported":true,"turns":3,...}
{"event":"agent_end","run_id":"agentrun-abc","ok":true}
```

`usage_reported` matters: a supplier that meters nothing leaves the figures
**absent rather than zero**, because `turns: 0` would be a claim about the run
rather than a gap. The gateway meters every call independently — its usage log
is the second observer, and the recorder compares the two.

## Exit codes

| | |
|---|---|
| 0 | done |
| 1 | the work failed |
| 2 | usage: no brief, a bad flag, an unreadable path |
| 3 | no configuration: the gateway credential did not project |

2 and 3 are separate from 1 on purpose. "My Secret did not project" and "the
work did not succeed" want different remedies, and collapsing them makes a
configuration bug look like a bad brief.
