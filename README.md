# leartech-ship-proven — ARCHIVED, and not the one you want

**Use `ship-proven agent` from
[leartech-ba-service](https://github.com/mikelear/leartech-ba-service).**
`brew install ship-proven` gets it; the agent images curl the same binary
from `storage.googleapis.com/downloads-product-first/ship-proven/`.

## Why this repo existed for one evening

It was created on the premise that the agent images could not fetch the
agent binary, because ba-service is private. **That premise was false.**
ba-service's release already cross-compiles the clients and publishes them
to a public GCS bucket, versioned and checksummed, which is where the
Homebrew tap points too. An image curls from the same path with the same
sha256, so the agent runs bit-for-bit what a developer runs.

The error came from two bad checks: a `grep` for brew that died on a shell
glob and whose empty output was read as evidence, and `gh release view`
showing no assets — true, because the artefacts go to GCS rather than to
GitHub releases.

## What was worth keeping went back

Three things here were genuine improvements on what ba-service had, and
they were ported:

- **`usage_reported`** — the turn count comes from the usage callback, so a
  supplier that meters nothing produced `turns: 0`, which reads as "never
  called a model" for work that demonstrably happened. The gateway's free
  `echo` adapter is exactly such a supplier. → ba-service #113
- **refusal counting** — a run declined forty tool calls and then reporting
  it could not finish looks like a model problem; the count identifies it
  as permissions. → ba-service #113
- **a distinct exit code for a missing credential** — so the controller can
  tell "my Secret did not project" from "the work failed". The estate
  already had `ErrNoCredential` and an exit-3 arm; `LoadConfig` just never
  produced the sentinel. → go-common #35

## What the estate decided, which still holds

- `leartech-go-common` — plumbing with no domain
- `leartech-dockerfiles` — images only; it builds applications *from* their
  repos, and its triggers fire per image directory
- private service repos — anything with a chart and a promotion

The fourth category this repo was meant to establish turned out to be
unnecessary for the agent, because the agent is a subcommand of a binary
that was already published.
