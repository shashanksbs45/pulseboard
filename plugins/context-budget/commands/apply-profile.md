---
description: Make a context-budget tool profile permanent by merging its deny list into Claude Code settings
argument-hint: "[lean] [local|project|user]"
---

Use the context-budget skill to apply a profile's deny list permanently.

Arguments: $ARGUMENTS — profile (only `lean` has a deny list; default `lean`) and scope
(default `local`):

| Scope | File | Shared with the team? |
| --- | --- | --- |
| local | `.claude/settings.local.json` | no (gitignored) |
| project | `.claude/settings.json` | yes, committed |
| user | `~/.claude/settings.json` | no, all projects |

1. Read the deny entries from the skill's `profiles/claude/settings.lean.json`.
2. Read the target settings file if it exists. Never drop or reorder existing keys or rules.
3. Show the user the entries that would be added (skip ones already present) and the file path,
   and ask for confirmation before writing. For `project` scope, point out it affects teammates.
4. On confirmation, merge into `permissions.deny` and write valid JSON.
5. Tell the user the change applies to new sessions, how to undo it (remove those entries),
   and that `/context-budget:audit` after a session confirms the tools are gone.
