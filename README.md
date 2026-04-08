# gxodus

Automate Google Takeout exports. Authenticate once, then schedule periodic data exports with a single command.

## Install

```bash
go install github.com/thinkjk/gxodus/cmd/gxodus@latest
```

Or build from source:

```bash
git clone https://github.com/thinkjk/gxodus.git
cd gxodus
make build
```

### Requirements

- Go 1.23+
- Chrome or Chromium installed

## Usage

### 1. Authenticate

```bash
gxodus auth
```

Opens a Chrome window for you to log into Google. Session cookies are encrypted and saved to `~/.config/gxodus/session.enc`.

### 2. Export

```bash
# Export all data, save ZIP archives
gxodus export

# Export to a specific directory
gxodus export --output ~/backups/google/

# Export and extract into organized folders
gxodus export --extract

# Custom poll interval
gxodus export --poll-interval 10m
```

### 3. Check status

```bash
gxodus status
```

## Configuration

Config file: `~/.config/gxodus/config.toml`

```toml
output_dir = "~/gxodus-exports"
poll_interval = "5m"
extract = false
keep_zip = true
file_size = "2GB"

[notify]
on_auth_expired = "ntfy publish gxodus 'Session expired, run: gxodus auth'"
on_export_complete = "ntfy publish gxodus 'Export done: {{.OutputPath}}'"
on_error = "ntfy publish gxodus 'Export failed: {{.Error}}'"
```

### Notification hooks

Shell commands executed on events. Template variables:

- `{{.Error}}` — Error message
- `{{.OutputPath}}` — Download directory
- `{{.ExportSize}}` — Total bytes downloaded
- `{{.Duration}}` — Time from start to completion

## Scheduling

```cron
# Export every 3 months
0 2 1 1,4,7,10 * /usr/local/bin/gxodus export --output ~/backups/gxodus/ --extract
```

Sessions expire after ~2 weeks of inactivity. For infrequent schedules, configure `on_auth_expired` notifications so you know when to re-authenticate.

## Docker

```bash
# Build
docker build -t gxodus .

# Authenticate (opens noVNC at http://localhost:6080)
docker run -it -p 6080:6080 -v ~/.config/gxodus:/config gxodus auth

# Export
docker run -v ~/.config/gxodus:/config -v ~/exports:/exports gxodus export

# Using an external browserless container
docker run -e GXODUS_REMOTE_CHROME=ws://browserless:3000 \
  -v ~/.config/gxodus:/config \
  -v ~/exports:/exports \
  gxodus auth
```

See `docker-compose.yml` for a complete example.

## Commands

| Command | Description |
|---------|-------------|
| `gxodus auth` | Interactive Google login |
| `gxodus auth --check` | Validate saved session |
| `gxodus auth --revoke` | Delete saved session |
| `gxodus auth --remote-chrome ws://...` | Use external Chrome |
| `gxodus export` | Run full export flow |
| `gxodus status` | Check export status |
| `gxodus version` | Print version |

## Exit codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Auth failure |
| 2 | Automation failure (UI changed) |
| 3 | Download failure |
| 4 | Configuration error |
