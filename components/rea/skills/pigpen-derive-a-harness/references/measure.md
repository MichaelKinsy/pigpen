# Measuring the piglet against the target

## Same script, both sides

Run the same steps on the target and on the piglet under the isolation described in [evidence.md](evidence.md), and
compare row by row. Useful rows, where the target has them:

- the screen after each step, text and style, at each terminal size
- each request to the model: prompt sections, tool list and schemas, messages
- each tool result
- the files written under the home and the working tree
- standard output, standard error and the exit status of one-shot runs
- the event or session log
- permission decisions: one row per tool call and variant, with whether a dialog appeared, its options and the outcome

Use REA's capture and comparison tools for the rows they cover, and plain scripts for the rest. Keep both sides' raw
output.

## Report rows, and class every row that differs

A single score hides what differs. For each differing row give the first difference, and put the row in one class:

| Class | Meaning |
|---|---|
| by design | the name, the logo, the piglet's own prompt wording, a recorded deliberate difference |
| needs the vendor's service | the target reaches a service the piglet cannot |
| host limit | PiG cannot do it; a written request exists |
| the target's own noise | differs between two runs of the target |
| the comparison's own paths | a directory name or cache path of the test setup |
| open | none of the above |

`open` is the work list. A row moves out of `open` only with a reason someone else could check.

## Changing the comparison

Mask only the target's own noise and the by-design identity, one written line per mask saying what is ignored and why.
Never add a mask to make a row pass, and never give a mask a number or a wording taken from the piglet.

## What a comparison of appearance cannot see

It passes when the piglet shows the right thing. It does not notice a setting nothing reads. So measure effects
separately:

- For every setting and flag in the inventory: set it on the piglet, run something it should change, and record the
  difference in behavior. No difference, then the row is `stored-only`.
- For a safety feature: try the thing it should stop.
- For a restriction: write it in each form the configuration allows and check that it holds.

## The mode the users are in

A stand-in model and an own-key mode make runs repeatable, and they are not what users run. Run the piglet in the
users' mode as well, with a real model and their kind of login, for at least: starting, switching model, every command
that exists only in that mode, one task that edits files, resuming a session. Features hidden in the stand-in mode are
tested nowhere else.

## Before you class a row

- Run it again alone. A machine under load produces differences that are gone in a quiet run; note the load when you
  measure.
- A permission row where the piglet asks less than the target is never noise. Fix it first.
- Compare against the previous measurement of the piglet too: a row that matched before and differs now is a
  regression until shown otherwise.
