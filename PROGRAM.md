TASK
=====

Automated implementation loop. Each cycle: sync approved GitHub issues into TASKS.md, then implement one task.

STEP 1: SYNC GITHUB ISSUES → TASKS.md
===

Fetch approved issues and update TASKS.md:

```bash
# List open issues with an "Approved" comment from lnsp
gh issue list --repo lnsp/wealth --state open --json number,title,body,comments --limit 50
```

Filter the results for issues where any comment from `lnsp` contains "approved" (case-insensitive).

Read TASKS.md. For each approved issue NOT already listed:
- Add it under `## Open` as: `- [ ] #<number>: <title>`

For any `## Open` task whose GitHub issue is now closed, move it to `## Completed` and mark `[x]`.

If there are no approved issues and no open tasks in TASKS.md, stop — output "No tasks. Idle." and end the cycle.

STEP 2: PICK A TASK
===

Read TASKS.md. Pick the top unchecked `- [ ]` item from `## Open`.

Fetch the full issue body for context:
```bash
gh issue view <number> --repo lnsp/wealth
```

Read CLAUDE.md and ARCHITECTURE.md only if the task involves new features or unfamiliar areas.

STEP 3: IMPLEMENT
===

Implement the task. Follow these rules:

- One task per cycle. Don't scope-creep.
- Read before writing. Understand existing code before modifying it.
- Run `go test ./internal/...` after backend changes.
- Run `cd frontend && npm test` after frontend changes (requires auth disabled in .env and seeded DB — see memory).
- Deploy via `sudo docker compose up -d --build` if changes affect the running app.

STEP 4: QC CHECK
===

After implementation, check your own work:

- If frontend was changed: spawn the **ui-auditor** agent on affected pages (desktop 1280x800 + mobile 375x812).
- Brand compliance: EB Garamond headings, Inter body, cream bg (#FAF9F6), no card borders, club palette charts, 8pt grid.
- Fix any issues the auditor finds before committing.

STEP 5: CONCLUDE
===

1. Mark the task `[x]` in TASKS.md
2. Update ARCHITECTURE.md only if new features were added
3. Git commit and push
4. Close the GitHub issue: `gh issue close <number> --repo lnsp/wealth`
