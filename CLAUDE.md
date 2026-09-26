# leartech-ship-proven

This project is wired into the Leartech hub at `~/leartech/hub/`.

## Hub status (loaded automatically)

@~/leartech/hub/status/leartech-ship-proven.md

This is a **PUBLIC application repo** — released by tagging, no Docker image
of its own, no JX promotion. `cmd/agent` is built into the per-language agent
images by `leartech-dockerfiles`, and `pkg/agentrun` is imported by
`leartech-ba-service` so `ship-proven agent` and the images run one
implementation rather than two that have to agree.

## Why it is public, and why it is not in go-common or dockerfiles

- **Public** because the agent images must fetch it at build time. Everything
  here is client-side: no secrets, no cluster addresses, no server code.
- **Not `leartech-go-common`** — that is plumbing with no domain, imported by
  six repositories. An agent's system prompt and gate are not plumbing, and
  putting them there would make agent churn bump unrelated services.
- **Not `leartech-dockerfiles`** — its Lighthouse triggers fire per image
  directory (`run_if_changed: "leartech-agent-go/.*"`), so an application
  shared by five images would never retrigger the builds that depend on it.
- **Not `leartech-ba-service`** — private, so an image could not fetch it.

## Rules

- The loop itself lives in `leartech-go-common/pkg/agentloop`. Do not
  reimplement turns, tools or the gate here; this repo is what an
  **unattended** run needs around that loop.
- `cmd/agent` stays thin. Logic there is logic the interactive front door in
  ba-service cannot reach, and the two then drift.
- No `login`, no browser OAuth, no key minting. The orchestrator-controller
  mints a budgeted key per AgentRun and projects it; an agent pod has no
  business carrying the code to mint one.
- Coverage scope is `./pkg/...`. `cmd/agent` is covered by its own dispatch
  tests instead — see `cmd/agent/main_test.go`, and the note in the Makefile.

## Updating the hub

When significant state changes during this session — new package, breaking
change, blocker hit — update `~/leartech/hub/status/leartech-ship-proven.md`
directly. Keep it concise (it loads into every future session here). Commit
and push so other sessions/machines see it.
