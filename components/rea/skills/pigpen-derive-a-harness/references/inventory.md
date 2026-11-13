# The inventory: every item of the surface, one row each

Without a list of what the target offers, nobody can say what the piglet lacks. Make the list before you build, and keep
it as the one place where completeness is stated.

## What to list

Take the kinds the target has; these are the usual ones for a harness, a CLI or a terminal app:

- commands with every argument form, and their aliases
- command-line flags, subcommands and the subcommands' flags, help topics
- environment variables
- settings keys, feature flags and experiments
- tools and their parameters; built-in agents and skills
- sections of the system prompt; how requests differ per model family
- keyboard shortcuts and mouse actions
- permission kinds and rules, modes and the transitions between them
- hooks and their events
- locations it reads configuration, instructions, plugins and extensions from
- files it writes (state, sessions, logs)
- output formats and exit statuses
- events and protocol methods it emits or serves

Go by the target's own tables where it has them (a command table, a settings schema, the help output): they are more
complete than what you happen to run into.

## A row

| Field | Content |
|---|---|
| item | the name a user would type or see |
| what the target does | in your words, short |
| evidence | `evidence_id` or file and field, and the kind (`observed`, `static`, `assumption`) |
| needs | nothing special, a service of the vendor, or a feature of the host (PiG) |
| weight | `blocker`, `major` or `minor`, by what a user loses without it |
| piglet status | see below, with its proof and the date it was checked |

## Status of the piglet on a row

| Status | Meaning |
|---|---|
| `same` | the piglet does what the target does |
| `differs` | present, and a user would see a difference; say which |
| `missing` | not there |
| `stored-only` | accepted, saved or shown, and nothing reads it: it has no effect |
| `n/a-service` | cannot work without the vendor's service; say which one |
| `not-checked` | not established; say why |

Rules for setting a status:

- Never set `same` because a name exists. For a setting or a flag, the proof is the code that reads it and acts on it,
  or an observed change in behavior.
- A status carries proof seen at the time it was set: a `file:line` you read, a row of a comparison, or the command you
  ran and the line it printed. No proof, then `not-checked`.
- A status is only as new as its date. After the build changes, the row is unknown again until re-checked.
- For `n/a-service`, write what the piglet does when the user reaches for the item. It should say plainly that the
  thing is not available, not fail obscurely and not pretend.

## Agree on the scope early

Some rows are whole subsystems: a sandbox for commands, a runtime for the target's plugins or extensions, hosted
features. Put them before the user as a short list with three choices each: build it, say plainly that the piglet does
not have it, or leave it out. Do this before building, and do not build the screens of a subsystem whose function is
not going to be built.
