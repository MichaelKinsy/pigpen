# Evidence that holds up

## Three kinds of claim

| Kind | Rests on | Says |
|---|---|---|
| `observed` | a capture of the target running (screen, request, file, exit status) | what it did in that run, in that mode |
| `static` | the target's code, strings or schemas | what exists; often not what it does |
| `assumption` | neither | your guess, with the limit that stopped you |

Mark every claim in `spec/` with its kind and its `evidence_id` (or the evidence file and field). An `assumption` that
later gets evidence is rewritten, not left beside the new claim.

## Read every result for what it did not cover

- An exit status of 0 is not "complete". Before you use a result, read its coverage: counters of skipped or unparsed
  inputs, a status such as `partial`, and the `limitations` list, per file as well as overall.
- A result that skipped an input says nothing about that input. Write "not analysed: <limitation>" in `spec/`; never
  write "the target has no X" from it.
- When a tool cannot finish on a large input (a deadline, a heap limit):
  1. narrow the scope first (one file, the source paths you need) at the default limits;
  2. raise a limit once if the tool names the limit that stopped it;
  3. then stop retrying. Read the extracted files directly (search for strings, read the schemas and the help text),
     mark those claims `static`, and record what stayed unanalysed.

## Static evidence finds names; behavior needs more

A string search or a schema gives you the name of a setting, a flag or a command, its help text and its allowed values.
It does not show that anything acts on it, or how. For each such item, before you write what it does:

- find the code that reads it and follow it to the effect, or
- change it on the running target and capture the difference.

If you can do neither, the claim is "exists; effect not established".

## Capturing the target

- **Isolate.** A scratch home and state directory, a clean environment, a private terminal server, a scratch working
  tree. Nothing the capture does may reach the user's real configuration.
- **Make it repeatable.** Fixed terminal sizes, the same typed input with the same pauses, and, where the target
  accepts its own model endpoint, a scripted stand-in model that answers the same way every time.
- **Capture the request instead of guessing.** The system prompt, the tool schemas and the message shapes come from one
  captured request to the model, not from reading the code.
- **Keep what you typed and when.** The script, the timings and the raw output all stay under `evidence/`.

## Modes

A target usually has several modes, and a capture shows one of them: signed in, own key or offline; interactive,
one-shot or server; experimental features on or off; each platform.

- List the modes first. Record the mode of every capture.
- A command or setting that is absent in the mode you captured may exist in another. Write "not captured in mode X",
  not "absent".
- Find out which mode the piglet's users will be in. If you cannot capture the target in that mode (no account, a
  refused subscription), say so at the top of the spec: it limits every later comparison.

## The target's own noise

Capture the same script on the target twice before you compare anything else with it. What differs between the two
runs is the target's own: an order that is random per process, which of two parallel things finishes first, relative
times, counters that depend on timing. Record each such row as noise with the two values. Do not chase it later, and do
not treat one of the two runs as the truth.

When the capture script changes, capture the target again. A reference made by an older script is not comparable.

## Safety while capturing

- Do not let the target act on real accounts or the machine's trust settings to see what happens: publishing or
  uploading, signing in or out, installing certificates, changing a keychain. Read the code path instead and mark the
  claim `static`.
- Ask the user before any capture that writes outside the scratch directories or contacts a service with their
  credentials.
