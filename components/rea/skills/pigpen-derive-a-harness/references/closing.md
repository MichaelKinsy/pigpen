# Closing

## The gate: re-check the inventory on the current build

Before you say the piglet is finished, or how finished it is, go through `spec/inventory.md` again against the build you
are about to deliver:

- Every row gets a status with proof seen now: the code that reads the item and acts on it, a row of the current
  measurement, or a command you ran on this build and the line it printed.
- Do not carry a status over from an earlier build, and do not take one from a builder's report.
- If you can, the check is done by someone, or a fresh session, that did not build the piglet. The contract for it:
  read-only on the piglet, one row per item, no proof means `not-checked`.
- Two checks that disagree on a row are both unproven until you have looked yourself.

A list older than the build is history, not a status. Re-check after every round of changes, not once.

## What to tell the user

- The inventory's counts per status, and the date and build of the re-check.
- The open rows by subject, most important first: what the user sees and what the target does.
- The decisions that are theirs: subsystems not built, services the piglet cannot reach, deliberate differences.
- What was not checked, and in which mode the target could not be captured.
- The comparison's numbers, labelled as what they are: rows compared that match.

## Deliberate differences

Keep one list in the spec: where the piglet asks more than the target, what it refuses to do, what it says instead of
doing. Each entry has the reason and, where it is a safety choice, a test.

## Safety review before delivery

Check, on the build to be delivered:

- what the model can reach without a dialog, and whether the target lets it;
- what the screen promises (enabled, restricted, sandboxed, private) against what is enforced;
- what is written outside the piglet's own state directory;
- what leaves the machine, to whom, and with which credentials;
- what the piglet starts as child processes and which environment they get.

## What you deliver

- The Binary, its size and hash, and the commit it was built from.
- The PiG version it needs, and any local patches to PiG it depends on.
- The platforms it was actually built and run on. The others are not a claim.
- The spec, the inventory with its last re-check, and the measurement's raw output.
