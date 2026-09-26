// Package agentrun runs one brief to completion with nobody at the keyboard.
//
// WHAT IT IS. An agent is a model given a set of tools, asked to do a piece
// of work, and allowed to call those tools until it is done.
// [github.com/mikelear/leartech-go-common/pkg/agentloop] is that loop; this
// package is everything around it that an unattended run needs and an
// interactive one does not:
//
//   - the gateway credential comes from the ENVIRONMENT, because the
//     orchestrator-controller mints a budgeted key per AgentRun and projects
//     it into the Job. Nothing here reads a session file or mints a key: a
//     Job has no browser;
//   - the brief comes from a file or the environment rather than a terminal;
//   - the gate's answers are pre-seeded, because an agent that had to ask
//     would deadlock on its first edit;
//   - the tool budget is a HARD ceiling, because there is nobody to raise it;
//   - what happened is emitted as JSON keyed on run_id, which is the join key
//     the gateway's usage rows, lokiserver's run_report and the recorder all
//     correlate on.
//
// WHY IT IS ITS OWN REPOSITORY. It is built into the per-language agent
// images, and those are built from leartech-dockerfiles, whose triggers fire
// per image directory — so an application shared by five images cannot live
// there without silently diverging from them. It cannot live in
// leartech-go-common either: that is plumbing with no domain, imported by six
// repositories, and an agent's system prompt is not plumbing. And it cannot
// live in leartech-ba-service, which is private, so an image could not fetch
// it.
//
// A public module that the images `go install` from a tag is the arrangement
// with none of those problems, and it is the same pattern leartech-agent-go
// already uses for golangci-lint and govulncheck.
//
// THE HUMAN FRONT DOOR IS ELSEWHERE. `ship-proven shell` in
// leartech-ba-service drives the same agentloop interactively and calls this
// package for its `agent` subcommand, so there is one implementation of what
// an unattended run does rather than two that have to agree.
package agentrun
