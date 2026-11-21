---
name: pigpen-audit-app-traffic
description: Find out what an app on the user's own machine sends over the network, where it goes, which requests carry the user's data, and which process and code likely sent them, from a traffic capture read with REA. Use when the user asks what an app sends or whether it "phones home", hands over a HAR, Fiddler or mitmproxy capture to read, or asks which requests carry their data and what sent them.
---

# Audit what an app sends

A capture shows what left the machine. REA reads the capture offline and never fetches a recorded URL. Tying a request
to the code that sent it is a second step, and most of it is inference. The report keeps the two apart.

Three failures are common, and the steps below exist to prevent them:

1. **"It sends nothing else" from a capture that could not see everything.** A proxy sees only the connections that
   went through it and trusted it. A program that ignores the proxy setting, or refuses the proxy's certificate, is
   missing or shows only a host name.
2. **"This code sent it" from a string match.** The capture reader does not attribute requests. A URL found in the
   app's files may be a help text. Without more, a match is a candidate.
3. **The user's data in the report.** A capture holds credentials and personal values. The audit locates them; it does
   not repeat them.

## Only this

- The user's own machine, an app they run, traffic of their own account.
- The proxy's certificate reaches the app through the app's environment, for that one process. Never add it to the
  system trust store or a keychain, never change the system proxy settings, and never ask the user to. An app that
  cannot be told to trust it per process stays unobserved, and the report says so.
- Do not defeat certificate pinning or other protections as part of this skill. Report the host as contacted and its
  content as not observed.
- Never replay a captured request.

## Workspace

Work in the current project directory, under `evidence/traffic/` (keep `evidence/` out of version control):

- `evidence/traffic/<run>/`: the proxy's private directory, its log, the capture and what you ran.
- `evidence/traffic/report.md`: the result.

A capture file contains what the app sent, credentials included. REA leaves known authentication headers and cookies
out of what it returns; the file on disk still has them. Do not copy bodies into the conversation beyond the field
names you need, and offer to delete the captures when the audit ends.

## The loop

| Step | What it produces | Read |
|---|---|---|
| 1. Get a capture | a HAR (or SAZ) of one named run, with what it could not see written down | [references/capture.md](references/capture.md) |
| 2. Read it | the destinations, grouped; the records in each group | [references/read.md](references/read.md) |
| 3. Locate the user's data | per value, the requests and fields that carry it | [references/read.md](references/read.md) |
| 4. Find the sender | the process (often observed), the code (usually a candidate) | [references/attribute.md](references/attribute.md) |
| 5. Report | `evidence/traffic/report.md` | below |

When the user already has a capture, start at step 2, and ask how it was made: which app, which actions, which tool.
Its blind spots are those of that tool and that run.

For the REA calls themselves follow the `pigpen-reverse-engineer-anything` skill (REA's own).

## Kinds of claim

Mark every line of the report with one of these:

| Kind | Rests on | Example |
|---|---|---|
| `observed` | the capture, the proxy's log, or a second capture after one change | "POST to `/v1/events` 14 times in this run" |
| `inferred` | a match in the app's files, or a pattern across records | "the path string occurs once, in `main.js`" |
| `not established` | neither | "which module builds the body" |

An `inferred` claim names what would make it `observed`. Nothing moves up a kind because it is plausible.

## Report

Write `evidence/traffic/report.md`:

1. **The run.** The app and its version, the mode (signed in or not, which settings), what was done in it, the capture
   tool and its version, the date.
2. **What the capture could not see.** Hosts whose connections refused the proxy's certificate, connections that did
   not go through the proxy, bodies that were not stored or could not be read (compressed, encrypted, binary).
   "Nothing found" in this list is a result; an empty list without having looked is not.
3. **Destinations.** One row per origin and path template:

   | Destination | Requests | When | Carries | Sender | Kind |
   |---|---|---|---|---|---|
   | `https://host/path/{numeric}` | count, record ordinals | at start, on which action | field names; which of the user's values, by label | process; file and position of a candidate | per cell |

4. **The user's data.** Per value, by its label ("email", "computer name"), never the value: the records and fields
   where it was found, and the spellings that were tried and not found.
5. **What to check next**, when a claim that matters is still `inferred`.

Give counts, not adjectives. Do not call a destination "tracking" or "safe"; say what it received.
