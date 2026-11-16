# From a request to what sent it

A HAR records requests. It does not record the process that made them or the code that built them, and REA's capture
reader does not add either. Work down this list; each level is weaker than the one before it.

## 1. Which process (often `observed`)

- **The `user-agent` header.** A helper the app starts usually names itself (`git/2.x`, `curl/8.x`, an updater). Two
  different user agents to one host are two senders.
- **The certificate variable it needed.** If a host's requests appeared only after one program's variable was added
  ([capture.md](capture.md)), that program made them.
- **The sockets.** While a request is in flight, the process that holds the connection to the proxy is its sender
  (`lsof -nP -iTCP -a -p <pids>`; the proxy's log gives the client port of each connection).

For a page in a browser, use REA's passive capture instead: it records what started each request. A HAR saved from the
browser's network panel may carry an `_initiator` field per entry; REA returns it as the producer reported it.

## 2. Which code (usually `inferred`)

Take what is distinctive in the request and search the app's files for it. In order of how much a hit says:

1. field names of the request body that are not ordinary words;
2. a header name the app adds itself;
3. literal segments of the path template (`/v1/telemetry/batch`, not `/api`);
4. the host name.

How to open the app's files depends on what it is; follow the REA skill: `references/javascript-applications.md` for a
bundle or an Electron app, `references/android-applications.md` for an APK, `references/native-and-artifacts.md` for a
native binary.

Read every hit before you count it:

- **A hit can be text the app shows.** A URL in a help page, an error message or a link is not the caller. The caller
  is code that passes the string to something that sends.
- **One hit in code that sends** is a candidate: name the file and the position, and call it `inferred`.
- **Several hits** are several candidates. Do not pick one.
- **No hit** means the string is built at run time, kept encoded, or lives in a part you did not open. It does not
  mean the app did not send the request; the capture says it did.

In a native binary a string match gives an address. Use the cross-references of that address to reach the function
that uses it before you name a function.

## 3. Make it `observed`

A candidate becomes a finding only through a second capture:

- **Switch it off.** Find the setting, flag or environment variable the candidate code checks, change only that, and
  capture the same run again. The requests that disappear depend on it.
- **Change an input.** If the body is said to carry a value, change that value on the machine (in a scratch home) and
  capture again; the field changes with it.
- **Remove the trigger.** If the request is said to belong to a feature, capture a run that does not use the feature.

Record each such pair of captures in the report. Where none was possible, the line stays `inferred` and says which
of these would settle it.

## What not to conclude

- "This function sends the user's email" from a string match and a redaction in the same request. The match places a
  string; the redaction places a value; nothing yet connects the two.
- "The app never contacts X" from an empty search. Only a capture that could see everything supports "did not, in this
  run".
- Intent. Report what was sent, to where, when and by what; leave why to the reader.
