# Network recovery for HTTP/2 PROTOCOL_ERROR

Source: PR [#84](https://github.com/tmonk/pi-goal-x/pull/84) by `rogeecn` (Rogee), included on maintainer branch `incoming/pr-84`. The work is on a maintainer branch rather than in the pull request because commits cannot be added to a pull request from outside the contributor's fork.

## Problem

An HTTP/2 stream failure surfaced by a provider as `stream error: stream ID 1; PROTOCOL_ERROR; received from peer` was not classified as transient. `isNetworkErrorAssistantMessage` matches a fixed list of provider failure text in `extensions/goal-format.ts`, and `PROTOCOL_ERROR` was absent, so a goal run hit the failure without the backoff that `extensions/network-error-backoff.ts` already provides for every other transport outage.

No upstream handling exists: neither `pi-ai` nor `pi-coding-agent` matches `PROTOCOL_ERROR` anywhere in their shipped code.

## Behavior

`PROTOCOL_ERROR` (and the `protocol error` spelling variants) joins the transient provider failure list. `NON_TRANSIENT_PROVIDER_ERROR_RE` is evaluated first, so quota and billing text still wins and is never retried. A regression test covers the reported Codex payload verbatim.

## Boundaries

No command, setting, tool, retry policy, or backoff parameter changes; the existing policy is reused. Prompt text and scheduler state are untouched, so no context baseline re-measurement is required. README and `specs/SPECS.yaml` are untouched; this directory is registered at merge time.