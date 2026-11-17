# Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, or nothing to do |
| 1 | Runtime or API error |
| 2 | `plan` / `drift` with `--detailed-exitcode` (or `sync --dry-run`) found changes. Not an error |
| 3 | Usage or configuration invalid: unknown command, bad flag or argument count, schema, references, secrets, missing API key or organization, named resource not found |
| 4 | Blocked: immutable change, deletion protection, unsafe scale-in, cluster turned off, ambiguous name, a confirmation with no terminal and no `-y`, credentials directory not git-ignored |
| 5 | Timed out waiting for Capella (the operation continues; re-run to resume waiting) |
| 130 | Interrupted |

Coming from capellactl: usage errors exited 2 there and exit 3 here; a confirmation without a
terminal exited 1 there and exits 4 here. See `cbctl capella guide migrate`.
