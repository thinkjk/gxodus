# gxodus Design Spec

## Context

Google Takeout is the only way to get a complete export of all your Google data in one shot. But it's a manual, tedious web process: click through the UI, wait hours/days, then manually download archives. There's no official API that covers everything Takeout does (the Data Portability API covers a subset, and major services like Drive/Gmail/Photos have separate APIs, but nothing unifies them like Takeout).

gxodus automates the entire Google Takeout flow end-to-end: authenticate, initiate an export of all services, poll until ready, download the archives, and optionally extract them. It's designed to be run on a schedule (cron/launchd) for periodic backups.

## Overview

- **Name:** gxodus ("Google data exodus")
- **Language:** Go
- **Type:** CLI tool (single binary)
- **Browser automation:** chromedp (Chrome DevTools Protocol)
- **Config format:** TOML
- **Output:** Local filesystem (cloud sync is out of scope; users can pair with rclone)

## Commands

### `gxodus auth`

Opens a visible Chrome window for the user to log into their Google account. Saves session cookies encrypted at rest.

- `gxodus auth` — Interactive login flow (opens visible browser or noVNC)
- `gxodus auth --check` — Validate saved session is still active (useful as a keep-alive cron job)
- `gxodus auth --revoke` — Delete saved session data
- `gxodus auth --remote-chrome ws://host:port` — Use an external Chrome/browserless instance for login

### `gxodus export`

Automates the full Takeout export flow in headless Chrome.

- `gxodus export` — Export all services, save ZIPs to default output dir
- `gxodus export --output /path/to/dir` — Specify output directory
- `gxodus export --extract` — Download and extract archives into date-stamped directories
- `gxodus export --poll-interval 10m` — Override poll interval (default: 5m)

### `gxodus status`

Check the status of the most recent export by loading the Takeout "manage exports" page.

- `gxodus status` — Opens headless Chrome, checks Takeout status page, prints current state (no local state file needed; reads directly from Google)

### `gxodus version`

Print version information.

## Architecture

```
cmd/
  gxodus/
    main.go              # Entry point
internal/
  cli/                   # Cobra command definitions
    root.go
    auth.go
    export.go
    status.go
  auth/                  # Session management
    session.go           # Cookie save/load/encrypt/decrypt
    keyring.go           # OS keychain integration for encryption key
  browser/               # chromedp automation
    browser.go           # Chrome lifecycle (launch, configure, shutdown)
    takeout.go           # Takeout-specific page automation
    login.go             # Google login page handling
  poller/                # Export status polling
    poller.go            # Poll loop with configurable interval
  downloader/            # Archive download
    downloader.go        # Download with resume support
  extractor/             # Optional ZIP extraction
    extractor.go         # Unpack into organized directories
  notify/                # Notification hooks
    notify.go            # Execute shell commands on events
  config/                # Configuration
    config.go            # TOML config parsing and defaults
```

## Components

### 1. CLI Layer (`internal/cli/`)

Cobra-based CLI. Each command is a separate file. Global flags: `--config`, `--verbose`.

### 2. Auth Manager (`internal/auth/`)

**Session storage:**
- Cookies serialized to JSON, encrypted with AES-256-GCM
- Encryption key stored in OS keychain (macOS Keychain via `keychain` package, Linux via `secret-service`/`kwallet`)
- Session file location: `~/.config/gxodus/session.enc`

**Session validation:**
- Load cookies into Chrome, navigate to `myaccount.google.com`
- Check if redirected to login page → session expired
- If valid, cookies are refreshed and re-saved

**Auth flow:**
1. Launch Chrome in visible (non-headless) mode
2. Navigate to `accounts.google.com`
3. User completes login manually (handles 2FA, CAPTCHAs, etc.)
4. chromedp polls the current URL; once it matches `myaccount.google.com` (post-login landing), login is considered complete
5. Extract all cookies for `.google.com` domains and save encrypted
6. Close browser

### 3. Takeout Driver (`internal/browser/`)

**Export initiation flow:**
1. Load saved cookies into headless Chrome
2. Navigate to `takeout.google.com`
3. Verify all services are selected (default behavior)
4. Click through export configuration:
   - Export once (not scheduled)
   - File type: ZIP
   - File size: 2GB (or configurable)
5. Click "Create export"
6. Capture any confirmation/job identifiers from the page

**Resilience:**
- Each step validates expected page state before proceeding
- Screenshots on failure for debugging (`~/.config/gxodus/debug/`)
- Descriptive errors identifying which step failed

### 4. Poller (`internal/poller/`)

- Navigates to Takeout's "manage exports" page periodically
- Checks export status via page DOM
- Default interval: 5 minutes, configurable via `--poll-interval` or config
- Logs status changes to stdout
- Exponential backoff on network failures (max 30 min between retries)

### 5. Downloader (`internal/downloader/`)

- Downloads archive files when export is complete
- Supports HTTP Range headers for resume on interruption
- Progress bar output to terminal
- Validates file integrity (size check against what Takeout reports)

### 6. Extractor (`internal/extractor/`)

- Activated by `--extract` flag
- Unpacks ZIPs into: `{output_dir}/{YYYY-MM-DD}/{service_name}/`
- Preserves original ZIPs alongside extracted content (unless `--no-keep-zip`)
- Handles multi-part archives (Takeout splits large exports)

### 7. Notification Hooks (`internal/notify/`)

Shell command templates executed on events:

```toml
[notify]
on_auth_expired = "ntfy publish gxodus 'Session expired, run: gxodus auth'"
on_export_started = ""
on_export_complete = "ntfy publish gxodus 'Export done: {{.OutputPath}}'"
on_error = "ntfy publish gxodus 'Export failed: {{.Error}}'"
```

Template variables:
- `{{.Error}}` — Error message
- `{{.OutputPath}}` — Download directory
- `{{.ExportSize}}` — Total size of downloaded archives
- `{{.Duration}}` — Time from initiation to completion

### 8. Config (`internal/config/`)

Location: `~/.config/gxodus/config.toml`

```toml
# Default output directory
output_dir = "~/gxodus-exports"

# Default poll interval
poll_interval = "5m"

# Auto-extract archives
extract = false

# Keep ZIP files after extraction
keep_zip = true

# Archive split size (matches Takeout options)
file_size = "2GB"

[notify]
on_auth_expired = ""
on_export_complete = ""
on_error = ""
```

## Error Handling

| Scenario | Behavior |
|----------|----------|
| Session expired | Exit code 1, fire `on_auth_expired` hook, log instructions to re-auth |
| Network failure during poll | Exponential backoff, retry up to 24 hours |
| Network failure during download | Resume from last byte on retry |
| Google Takeout UI changed | Fail with descriptive error + screenshot, exit code 2 |
| Chrome not found | Error message with install instructions |
| Disk full during download | Fail with clear error, partial files preserved for resume |

## Exit Codes

- `0` — Success
- `1` — Auth failure (session expired or not found)
- `2` — Automation failure (UI changed, element not found)
- `3` — Download failure
- `4` — Configuration error

## Scheduling Example

```cron
# Export every 3 months (1st of Jan, Apr, Jul, Oct)
0 2 1 1,4,7,10 * /usr/local/bin/gxodus export --output ~/backups/gxodus/ --extract
```

Since the session will likely expire between 3-month runs, the expected flow is:
1. Cron fires `gxodus export`
2. Session is expired → `on_auth_expired` hook fires notification
3. User sees notification, runs `gxodus auth` manually
4. User runs `gxodus export` manually (or waits for next cron)

## Dependencies

- `github.com/chromedp/chromedp` — Chrome automation
- `github.com/spf13/cobra` — CLI framework
- `github.com/pelletier/go-toml/v2` — TOML config parsing
- `github.com/zalando/go-keyring` — OS keychain access
- `github.com/schollz/progressbar/v3` — Download progress bars

## Distribution

### Binary

Single Go binary. Requires Chrome/Chromium installed on the host.

```bash
# Install
go install github.com/jason/gxodus/cmd/gxodus@latest

# Or download from GitHub releases
```

### Docker

For headless environments like Unraid, a Docker image bundles gxodus + Chromium:

```dockerfile
FROM chromedp/headless-shell:latest
COPY gxodus /usr/local/bin/gxodus
ENTRYPOINT ["gxodus"]
```

Usage:
```bash
# Auth via built-in noVNC (access at http://host:6080)
docker run -p 6080:6080 -v ~/.config/gxodus:/root/.config/gxodus gxodus auth

# Auth via external Chrome/browserless instance
docker run -v ~/.config/gxodus:/root/.config/gxodus gxodus auth --remote-chrome ws://browserless:3000

# Export (fully headless)
docker run -v ~/.config/gxodus:/root/.config/gxodus -v ~/exports:/exports gxodus export --output /exports
```

**Auth on headless servers (Unraid):**
- **Built-in noVNC:** The Docker image includes a lightweight noVNC server. Running `gxodus auth` exposes port 6080 — open it in any browser on your network to complete Google login.
- **External Chrome:** Use `--remote-chrome ws://host:port` to connect to an existing browserless/Chrome container. gxodus drives that browser for login instead of launching its own.

For Unraid: can be set up as a Docker container with User Scripts plugin for scheduling, or as a Community Applications template.

## Out of Scope (v1)

- Cloud storage sync (use rclone)
- Built-in scheduling daemon
- Service selection (always exports all)
- GUI
- Multi-account support
- Google Workspace/enterprise accounts
