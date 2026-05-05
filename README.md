# gxodus

Automated Google Takeout exports. Authenticate once via the browser, then a long-running container creates an export, polls until Google finishes, and downloads the archives — repeating on whatever schedule you set.

Designed to run unattended on a NAS (Unraid, Synology, etc.) for a recurring "set-and-forget" backup of your full Google account. Surfaces re-auth requests via noVNC + Pushover when Google rotates the session.

## How it works

- **Auth** is interactive (one-time): Chromium opens against the container's Xvfb display, you log into Google via noVNC, session cookies get encrypted to `$CONFIG_DIR/session.enc`.
- **Create / poll / list** uses Google Takeout's internal `batchexecute` HTTP API directly (cookies-only). Fast, no browser needed at runtime.
- **Download** uses chromedp against the same persistent Chromium profile because the takeout download URL requires a fresh re-authentication token (`rapt`) that cookies alone can't supply. The first file in each cycle may prompt for a password challenge in noVNC; subsequent files in the same session reuse the rapt automatically.
- **Resume on restart** — the export's UUID is persisted to `$CONFIG_DIR/pending_export.uuid` after creation, so a container restart mid-poll picks up where it left off instead of submitting a new (rate-limited) export.
- **Auto-recovery** — when the saved cookies stop working, gxodus detects the redirect to Google sign-in, fires `auth_expired` (Pushover + shell hook), exits, and the entrypoint loop wipes `session.enc` and re-runs the auth flow on the next cycle so Chromium opens in noVNC for you to log in again.

## Quick start (Docker)

```bash
docker run -d \
  --name gxodus \
  -p 6080:6080 \
  -v ~/gxodus/config:/config \
  -v ~/gxodus/exports:/exports \
  -e GXODUS_INTERVAL=180d \
  -e GXODUS_PUSHOVER_TOKEN=your-app-token \
  -e GXODUS_PUSHOVER_USER_KEY=your-user-key \
  ghcr.io/thinkjk/gxodus:main
```

On first run there's no saved session, so the entrypoint launches Chromium for the auth flow. Open `http://<host>:6080/vnc.html`, log into Google, the browser closes automatically once cookies are extracted.

After that, gxodus enters its loop:
1. Create export → persist UUID to `pending_export.uuid`
2. Poll fhjYTc every hour until Google reports complete
3. Drive Chromium through the download URLs (clear the one-time re-auth challenge in noVNC if Pushover pings you)
4. Move archives into `/exports`
5. Sleep `GXODUS_INTERVAL` (e.g. `180d`), then repeat

A `docker-compose.yml` and an example `.unraid-template.xml` live in the repo.

## Quick start (native binary)

```bash
go install github.com/thinkjk/gxodus/cmd/gxodus@latest

# One-time auth (opens local Chromium)
gxodus auth

# Run an export (create + poll + download + extract)
gxodus export --extract --output ~/google-backups
```

Requires Go 1.26+ and a local Chromium / Chrome.

## Configuration

Two ways to configure: `config.toml` or environment variables. Env vars override the file when both are set. All paths default to `$XDG_CONFIG_HOME/gxodus` (or `~/.config/gxodus`); override with `GXODUS_CONFIG_DIR` (Docker default `/config`).

### config.toml

```toml
output_dir   = "/exports"          # GXODUS_OUTPUT_DIR
poll_interval = "1h"               # GXODUS_POLL_INTERVAL
extract       = false              # GXODUS_EXTRACT=true
keep_zip      = true               # GXODUS_NO_KEEP_ZIP=true to delete after extract
file_size     = "2GB"              # GXODUS_FILE_SIZE  — Google's archive split size
file_type     = "zip"              # zip | tgz
frequency     = "once"             # once | every_2_months
activity_logs = true               # include the "Access Log Activity" product

[notify]
on_auth_expired    = "ntfy publish gxodus 'Re-auth needed: open noVNC'"
on_export_complete = "ntfy publish gxodus 'Export done: {{.OutputPath}}'"
on_error           = "ntfy publish gxodus 'Export failed: {{.Error}}'"

[notify.pushover]
token    = "<your app token>"
user_key = "<your user key>"
# events = ["auth_expired", "export_complete", "error"]   # default; "export_started" opt-in
```

### Environment variables

Useful for Unraid template fields and docker-compose `environment:` blocks. Non-empty env vars win over config.toml values.

| Variable                   | Purpose |
|----------------------------|---------|
| `GXODUS_CONFIG_DIR`        | Where session.enc, config.toml, pending_export.uuid live (default `/config` in Docker) |
| `GXODUS_OUTPUT_DIR`        | Where downloaded archives land (default `/exports` in Docker) |
| `GXODUS_INTERVAL`          | Sleep between exports in container loop mode (e.g. `180d`, `7d`, `1h`). Unset = one-shot. |
| `GXODUS_AUTH_RETRY`        | Sleep between auth-failure retries (default `5m`) |
| `GXODUS_FILE_SIZE`         | Archive split size (`1GB`, `2GB`, `4GB`, `10GB`, `50GB`) |
| `GXODUS_FILE_TYPE`         | `zip` or `tgz` |
| `GXODUS_FREQUENCY`         | `once` or `every_2_months` |
| `GXODUS_POLL_INTERVAL`     | How often to check if Google's done preparing the export (default `1h`) |
| `GXODUS_EXTRACT`           | `true` to unzip after download |
| `GXODUS_NO_KEEP_ZIP`       | `true` to delete the .zip after a successful extract |
| `GXODUS_NO_ACTIVITY_LOGS`  | `true` to skip the (large) Access Log Activity product |
| `GXODUS_PUSHOVER_TOKEN`    | Pushover app token (built-in notification destination) |
| `GXODUS_PUSHOVER_USER_KEY` | Pushover user key |
| `GXODUS_PUSHOVER_EVENTS`   | Comma-separated event list (default `auth_expired,export_complete,error`) |
| `GXODUS_PUBLIC_HOSTNAME`   | Override the hostname in Pushover messages (the noVNC URL hint) |
| `GXODUS_COMMAND`           | Override the entrypoint subcommand (default `export`; useful values: `auth`, `status`) |

### Notification events

Both `[notify].on_*` shell hooks and `[notify.pushover]` fire from the same events:

| Event             | When |
|-------------------|------|
| `auth_expired`    | Cookies are stale OR a download URL hit a re-auth challenge that needs the user via noVNC |
| `export_started`  | Every time `CreateExport` succeeds (opt-in for Pushover — noisy on a 180-day cadence) |
| `export_complete` | All archives downloaded successfully |
| `error`           | Any other failure |

Shell-hook templates support `{{.Error}}`, `{{.OutputPath}}`, `{{.ExportSize}}`, `{{.Duration}}`. Pushover messages are baked-in (no template knobs in v1).

## Recovery and operations

### Container restarts mid-export

The pending UUID survives in `$CONFIG_DIR/pending_export.uuid`. On the next start, gxodus skips `CreateExport` and resumes polling/downloading the existing export. The marker is cleared only after a successful download.

### Skipping CreateExport for a known-good UUID

```bash
gxodus export --export-uuid 5430dfbb-4e4a-44e7-9d69-278cb5708616
```

Or pre-populate the marker before restarting the container:

```bash
docker exec gxodus sh -c 'echo <uuid> > /config/pending_export.uuid'
docker restart gxodus
```

### Re-authenticating without restarting the container

```bash
docker exec -it gxodus gxodus auth --config /config/config.toml
```

Chromium opens in noVNC at `<host>:6080/vnc.html`; log in once and cookies refresh.

### Hidden debug commands

| Command | Purpose |
|---------|---------|
| `gxodus debug-tokens` | Fetch the takeout page and print extracted tokens + cookie names |
| `gxodus debug-list` | Pretty-print all exports visible in the account |
| `gxodus debug-create --products drive` | One-shot create with simple flag-based args |
| `gxodus debug-download --uuid <uuid>` | Skip create+poll, exercise just the download path against an existing export |
| `gxodus debug-api --rpcid X --args '[...]' --version generic` | Raw escape hatch for any batchexecute rpcid |

These are `Hidden: true` so they don't show in `--help`. Look at `internal/cli/debug_api.go` for the full set.

## Exit codes

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | Auth failure or session expired (entrypoint wipes session and re-runs auth) |
| 2    | CreateExport / API failure |
| 3    | Poll / download failure |

## Architecture notes

Protocol details, batchexecute reverse-engineering, and the chromedp downloader design are in `docs/superpowers/specs/` and `docs/spikes/`. The relevant entry points:

- `internal/takeoutapi/` — batchexecute client, request encoding, response parsing, `CreateExport`/`ListExports`/`GetExport`
- `internal/poller/` — UUID-aware poll loop with `ErrSessionExpired` short-circuit
- `internal/downloader/` — chromedp-driven download with magic-bytes guard, EXDEV fallback, and challenge-detection auto-recovery
- `internal/browser/` — chromedp context/profile management used by both the auth and download paths
- `internal/notify/` — shell-hook + Pushover dispatch
- `internal/cli/` — Cobra commands; `debug_api.go` holds the (hidden) operator-grade helpers

## License

See [LICENSE](LICENSE).
