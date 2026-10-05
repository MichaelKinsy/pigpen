# Investigating a report

Technique for a maintainer working a bug report or pricing a gate change against this package, distilled from the sessions cited inline.
The per-issue narratives live in `docs/retro/`; this page keeps the reusable rule so it is not re-derived from them.

## Reproduce a bypass claim live

When a report claims a path/permission **bypass** (or that a rule is evadable), or reports a concrete prompt or decision the gate should not have produced, reproduce the literal repro against the running extension before concluding it is already handled — a live decision is stronger evidence than unit tests and can surface adjacent bugs (#493's bypass claim was already fixed, but the live repro exposed a misleading prompt, filed as #507; #712's yolo repro confirmed the report and exposed an unrelated deny-masking hole in the same branch).

## Mine the review log

To quantify a proposed gate change's blast radius, mine the local review log (`~/.pi/agent/extensions/pi-permission-system/logs/pi-permission-system-permission-review.jsonl`) — each `toolName: "bash"` entry carries the unredacted `command`, so a `node -e` scan over the deduplicated set yields a measured percentage instead of an estimate (#694: 2767 commands, three competing options).
Since #746 a command longer than `reviewLogFieldMaxWidth` (1000) is stored shortened with a trailing `…`, so filter `command.endsWith("…")` out before parsing — a truncated command re-parses as garbage, and #742's planning read 111 `ERROR` parses where the true count was 1 (measured: 4.3% of command entries).
The same log answers diagnostic questions: counting an `event` per day and against an adjacent event's timestamps separates populations a code reading treats as one (#727: 43 identical warnings split into 15 relay false alarms and 23 genuine misconfigurations).
A long-lived JSONL log is a schema-drift surface: entries from 2026-08-17 carry `surface`/`matchedPattern` and no `message`, so a `message`-keyed scan silently drops them.
Validate a scan against a raw sample from each era before aggregating, and commit the script beside any number a durable record cites (Refs #639).

A rule's zero hits in the log bound nothing about its false positives.
When over-matching would drop a projection (fail-open), run benign inputs built to trip the rule through the real `BashProgram.parse` before accepting it (#995: `grep HOME ~/.bashrc` under a rule matching any `HOME` argument).

The review log records decisions, so it cannot price a change that newly prompts on commands the gate allowed silently; the session transcripts hold every `bash` tool call the agent ran (#992: 70,961 unique commands):

```bash
find ~/.pi/agent/sessions -name '*.jsonl' -print0 | xargs -0 cat \
  | jq -r 'select(.type=="message" and .message.role=="assistant") | .message.content[]? | select(.type=="toolCall" and .name=="bash") | .arguments.command // empty | @json' | sort -u
```
