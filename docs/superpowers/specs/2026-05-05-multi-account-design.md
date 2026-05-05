# Multi-Account Backups Design

**Date:** 2026-05-05
**Status:** approved

## Problem

gxodus today assumes a single Google account. One `session.enc`, one
`chrome-profile`, one `pending_export.uuid`, one output dir, `userIdx=0`
hard-coded everywhere. Users with more than one account (work + personal,
spouse's account, etc.) have to run a separate gxodus container per
account, duplicating volumes and noVNC ports.

We want one container that backs up multiple accounts on the same
schedule, with one Pushover destination, one set of exports landing in
per-account subdirs.

## Decision

Each account is a separate Google sign-in (own cookies, own chromium
profile, own pending-export marker). Accounts run sequentially within
each cycle (same interval for all). Account identity is the email
extracted from the chromium session after login. Output lands in
per-account subdirs.

No backwards-compatibility code: this is a hard cutover, since gxodus
has no deployed users beyond the original author. The README will
document the new layout.

## On-disk layout

```
$CONFIG_DIR/                              # /config in Docker
  config.toml                             # global; output_dir, poll_interval, notify, etc.
  accounts/
    jason@example.com/
      session.enc                         # encrypted cookies
      chrome-profile/                     # persistent chromium profile
      pending_export.uuid                 # in-flight export marker
    work@example.com/
      session.enc
      chrome-profile/
      pending_export.uuid
  debug/                                  # shared (page dumps, rpc dumps)
  .failed-accounts                        # newline-separated emails (entrypoint reads on exit-1)

$OUTPUT_DIR/                              # /exports in Docker
  jason@example.com/
    takeout-2026-05-05-001.zip
    ...
  work@example.com/
    takeout-2026-05-05-001.zip
```

Account list is **implicit**: anything under `$CONFIG_DIR/accounts/<dir>/`
containing a `session.enc` counts as an account. Add by running
`gxodus auth --new`; remove by `gxodus remove-account <email>` (or
`rm -rf` the dir).

## Components

Five touched packages plus three new CLI commands.

**`internal/auth`** — `SessionPath` / `SaveSession` / `LoadSession` /
`SessionExists` / `DeleteSession` lose their hard-coded path. Each gains
an `accountDir string` argument. New helper
`auth.AccountDir(email string) string` returns
`filepath.Join(config.ConfigDir(), "accounts", email)`.

**`internal/browser`** — `ProfileDir()` becomes `ProfileDir(accountDir string)`.
`InteractiveLogin` gains an "extract email" step after login: navigate
to `https://myaccount.google.com/`, scrape the displayed email via
`chromedp.Text(emailSelector, &email)`. Returns `(cookies, email, error)`.
On scrape failure the cookies are saved to
`$CONFIG_DIR/.pending-auth-<unix>/` and the user is told which directory
to rename — never fail catastrophically after the user already typed a
password.

**`internal/cli/pending_export.go`** — `pendingExportPath`,
`readPendingExport`, `writePendingExport`, `clearPendingExport` all gain
an `accountDir string` argument.

**`internal/notify`** — `EventData` gains `Account string`. Built-in
Pushover messages prepend `[<account>]` to the title (e.g.
`gxodus: re-auth needed [jason@example.com]`). Shell-hook templates
support `{{.Account}}`.

**`internal/cli/export.go`** — `RunE` becomes "iterate over accounts → for
each, run the existing pipeline scoped to that account's dir". A new
function `runExportForAccount(ctx, email, cfg, deps)` wraps the pipeline.
A small interface (e.g. `takeoutClient`) replaces the direct
`takeoutapi.NewClient` call so the per-account isolation behavior is
unit-testable.

**New CLI commands:**

- `gxodus auth` — refresh whichever single existing account is
  configured. Errors if 0 or 2+ accounts exist (use `--new` or
  `--account`).
- `gxodus auth --new` — add a new account. Spawns chromium with a fresh
  temp profile (no existing account's profile), extracts email after
  login, creates `accounts/<email>/`.
- `gxodus auth --account <email>` — refresh a specific existing account
  (uses its existing chrome-profile so trusted-device state persists).
  If the dir doesn't exist, treat as adding (uses email-from-flag
  instead of email-from-page-scrape; user signals intent via the flag).
- `gxodus list-accounts` — prints emails + status (cookies present,
  marker file present, last export size+date if extractable).
- `gxodus remove-account <email>` — `rm -rf $CONFIG_DIR/accounts/<email>`
  and (by default) `$OUTPUT_DIR/<email>`. `--keep-exports` skips the
  output dir. Refuses if a download is in flight; `--force` overrides.

Existing commands (`status`, `export`, `debug-*`) gain `--account <email>`.
Default behavior: `export` iterates all accounts; `status` and the
hidden `debug-*` commands target the first account and warn if multiple
exist.

## Data flow

### First account (no `accounts/` dir yet)

```
docker run -d gxodus           → entrypoint: gxodus export
gxodus export                  → no accounts/ dir → exit 1, fire auth_expired (no email)
entrypoint                     → wipes session.enc (no-op) → sleep AUTH_RETRY (5m)
                               → next iteration: still no accounts → runs gxodus auth
gxodus auth                    → no existing accounts → runs as if --new
                               → spawns chromium with TEMP profile
user                           → logs in via noVNC
gxodus auth                    → navigates to myaccount.google.com → scrapes email
                               → mkdir accounts/<email>/, moves temp profile in
                               → saves cookies → session.enc
entrypoint                     → next gxodus export iterates accounts/<email>/
                               → CreateExport → poll → chromedp Download
                               → /exports/<email>/takeout-*.zip
```

### Adding a second account

```
docker exec -it gxodus gxodus auth --new
                               → spawns chromium with FRESH temp profile
                               → user logs in to account B → email scraped
                               → new accounts/<emailB>/ created
docker restart gxodus          → next cycle iterates BOTH accounts sequentially
```

### Per-cycle iteration

```go
failed := []string{}
for _, accountDir := range scanAccounts(config.ConfigDir()) {
    email := filepath.Base(accountDir)
    err := runExportForAccount(ctx, email, cfg)
    if err != nil {
        // logs error, fires notify with EventData.Account=email
        failed = append(failed, email)
    }
}
if len(failed) > 0 {
    writeFailedAccountsFile(failed)
    os.Exit(1) // entrypoint reads .failed-accounts and wipes only those sessions
}
sleep(GXODUS_INTERVAL)
```

### Failure modes mapping to exit codes

- 0 = all accounts succeeded
- 1 = ≥1 account had `ErrSessionExpired` (entrypoint wipes only listed
  sessions, then runs `gxodus auth --account <email>` for each in turn
  on subsequent loop iterations, with `AUTH_RETRY` backoff between)
- 3 = ≥1 account had a non-auth failure (entrypoint logs and continues
  next cycle without wiping anything)

If both 1 and 3 conditions are present, exit 1 (auth recovery is the
more urgent path — the 3-failures will be retried next cycle anyway).

## Error handling

| Scenario | Behavior |
|---|---|
| `accounts/` dir missing (fresh install) | Treat as no accounts → exit 1 → entrypoint runs `gxodus auth` |
| `accounts/<email>/` dir exists but no `session.enc` | Skip with warning; suggest `gxodus auth --account <email>`; continue iteration |
| Account A session expired, B valid | Run B normally; A fires `auth_expired` notify with `Account=A`; A's email written to `.failed-accounts`; entrypoint wipes only `accounts/A/session.enc` |
| Multiple accounts simultaneously expired | All listed in `.failed-accounts`; entrypoint wipes them all and runs `gxodus auth --account <email>` for the first one (subsequent ones get prompted on next cycle, AUTH_RETRY between) |
| `gxodus auth` (no flag) with 2+ existing accounts | Error: "use `--new` to add an account or `--account <email>` to refresh" |
| `gxodus auth --account <email>` and dir doesn't exist | Treat as adding (user signaled intent via flag); use email-from-flag instead of page-scrape |
| Email scrape fails (page changed, network glitch) | Save cookies to `$CONFIG_DIR/.pending-auth-<unix>/`; print clear instructions to manually rename to the right `accounts/<email>/`. Never fail after the user typed a password. |
| `gxodus remove-account <email>` mid-flight | Refuse if marker file present; `--force` overrides |

`gxodus list-accounts` example output:

```
EMAIL                    SESSION    PENDING                       LAST EXPORT
jason@example.com        ✓ valid    -                             2026-05-04 4.46 GB
work@example.com         ✓ valid    5430dfbb-... (in flight)      -
spouse@example.com       ✗ stale    -                             2026-04-12 1.21 GB
```

(Status reflects only on-disk state — no Google API calls. "stale"
means the dir exists without a session.enc, OR a marker file points to
a UUID that's been failing to poll.)

## Testing

| Layer | Strategy |
|---|---|
| `auth.AccountDir(email)` + path helpers | Unit tests, table-driven (valid emails, `+aliases`, unicode local-parts, edge cases) |
| `SessionPath`/`SaveSession`/`LoadSession` parameterized by accountDir | Round-trip cookies through encrypt/decrypt for two different account dirs in `t.TempDir()`; verify isolation |
| Account discovery (scan `$CONFIG_DIR/accounts/*/`) | Unit tests: empty → `[]`; one → one; account-without-session-enc surfaces in `list-accounts` but is skipped by `export` |
| `EventData.Account` through `notify.Fire` | Extend existing httptest-backed Pushover test — assert title includes email when set, omits gracefully when blank |
| `runExportForAccount` isolation | Refactor introduces a small `takeoutClient` interface; test with fake client where A returns `ErrSessionExpired`, B succeeds → assert exit-1, `.failed-accounts` contains only A, B's marker cleared |
| `gxodus list-accounts` formatting | Unit test the row-builder pure function separately from FS access |
| Email extraction from chromium | Manual smoke test (Google DOM is theirs to change). Log clearly on empty scrape |
| End-to-end multi-account | Manual: `gxodus auth --new` twice, restart, observe sequential cycles in container logs; verify both `/exports/<email>/` populate |

The `runExportForAccount` interface refactor is in-scope cleanup since we're
touching `export.go` extensively anyway.

## Config-file changes

`config.toml` is unchanged in v1 (no per-account overrides — same
poll_interval, file_size, etc. for all accounts). If per-account knobs
are needed later, extend with:

```toml
[accounts.jason@example.com]
file_size = "10GB"
```

Out of scope for this spec.

## Out of scope

- **Parallel downloads across accounts.** Sequential is sufficient and avoids
  noVNC challenge collisions.
- **Per-account schedules.** Same `GXODUS_INTERVAL` for all.
- **Per-account config overrides.** Single config block applies to all.
- **Backwards-compatibility with single-account layout.** Hard cutover.
- **Web UI for account management.** CLI only.

## Migration

For the original author's current single-account install:

1. `docker exec gxodus gxodus list-accounts` → empty (new code, no
   accounts/ dir yet)
2. Manually move existing files:
   ```sh
   docker exec gxodus sh -c '
     mkdir -p /config/accounts/jason@example.com
     mv /config/session.enc /config/chrome-profile /config/pending_export.uuid \
        /config/accounts/jason@example.com/ 2>/dev/null
   '
   ```
3. Restart container — cycle picks up the moved account naturally.

The README will document this.

## Migration pitfalls

- Email must be known before move (no auto-detect from session.enc since
  cookies don't directly encode the email — only numeric user ID is in
  fhjYTc responses). Worst case: pick any name, then run
  `gxodus auth --account <real-email>` once to refresh and let the
  scrape land the real email; rename the dir to match.
- `pending_export.uuid` survives the move; the resumed export will
  download to the correct per-account `/exports/<email>/` dir.
