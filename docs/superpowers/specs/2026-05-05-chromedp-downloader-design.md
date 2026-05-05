# chromedp Downloader Design

**Date:** 2026-05-05
**Status:** approved

## Problem

gxodus's HTTP downloader (`internal/downloader/downloader.go`) sends GET
requests to `https://takeout.google.com/takeout/download?j=…&i=…&user=…`
with the user's session cookies. Google rejects cookie-only auth on this
endpoint and redirects to `https://accounts.google.com/v3/signin/challenge/pwd`
with the original download URL in `continue=`. After the user completes a
fresh password challenge, Google appends a `rapt` (re-authentication token)
param to the URL and the actual archive download proceeds.

Web research confirmed that no programmatic API path exists to obtain a
`rapt` for email-link delivery. Drive delivery (`Add to Drive`) bypasses
`rapt` but counts against Drive quota — a non-starter for a 550 GB export.

The only viable path is to render the download in a real browser, where the
persistent profile, cookies, and any required password challenge can all
work together. The container already ships chromium + noVNC for the initial
auth flow, so the runtime cost is zero.

## Decision

Replace the HTTP downloader entirely with a chromedp implementation. Reuse
the existing `internal/browser` package (which already has `NewContext`,
`InjectCookies`, and `ProfileDir`). The persistent profile means Google
treats the container as a known device, so most downloads will not trigger
a fresh challenge. When a challenge does appear, the browser (which is already running
headed against the container's Xvfb display) is visible via noVNC, fires
the `auth_expired` notify event (which now triggers both the user's shell
hook and a built-in Pushover message — see the Pushover section below),
and blocks indefinitely until the URL leaves the challenge page.

## Architecture

```
internal/downloader/
  downloader.go      ← rewritten: Download(urls, outputDir, cookies, notifyCfg)
                       (signature gains notifyCfg so we can fire pushover
                       on challenge)
```

Caller (`internal/cli/export.go`) keeps the same one-line invocation, with
the new `notifyCfg` argument threaded through. No new packages.

## Components

Three pieces inside `downloader.go`:

1. **Browser bootstrap.** Open a chromedp context with `browser.ProfileDir()`
   and `Headless=false` (the container's Xvfb is already up 24/7 for the
   auth flow's noVNC session, so headed costs nothing extra and means a
   challenge is immediately visible without a context swap). Inject the
   saved session cookies. Configure CDP `Browser.setDownloadBehavior` to
   drop files into a known temp dir at `$CONFIG_DIR/downloads-tmp/`. Empty
   that dir first so abandoned partials from a previous container don't
   confuse us.

2. **Per-URL download loop.** For each URL: navigate, listen for CDP
   `Browser.downloadProgress` events. On `downloadWillBegin` log the
   filename + total bytes. On `state="completed"` move the file from the
   tmp dir to the final output dir via `os.Rename` (atomic within the same
   filesystem). Run the magic-bytes check on the final file as a defense-in-
   depth sanity check.

3. **Challenge detector.** After navigating, race three outcomes for ~10s:
   (a) `downloadWillBegin` event — happy path; (b) URL still off
   `takeout.google.com` — challenge; (c) navigation error — bubble up. On
   challenge: the headed browser is already visible via noVNC, so just
   fire `auth_expired` notify, log the noVNC URL, then block on
   chromedp's URL-watch action until the URL host returns to
   `takeout.google.com`. No timeout: Google's URLs stay valid for ~7 days
   and the user has noVNC + pushover.

## Data flow

```
poller returns []DownloadURLs (e.g. 5 URLs for a 4.46 GB export)
         │
         ▼
Download(urls, outputDir, cookies, notifyCfg)
  ├── ensure $CONFIG_DIR/downloads-tmp/ exists, empty
  ├── browser.NewContext(headless=false, UserDataDir=ProfileDir())
  ├── InjectCookies(sessionCookies)
  ├── CDP: setDownloadBehavior(allow, downloadPath=tmp)
  │
  └── for each url:
       ├── chromedp.Navigate(url)
       ├── race:
       │     A: CDP downloadWillBegin event   (happy path)
       │     B: 10s elapse + URL still off-takeout (challenge)
       │     C: navigation error
       │
       ├── if A → wait for downloadProgress.state="completed"
       │         → os.Rename(tmp/file, outputDir/file)
       │         → magic-bytes check (defense-in-depth)
       │         → append to result.Files
       │
       └── if B → notify.Fire("auth_expired", {...})
                → log: "Download blocked on re-auth challenge —
                       open noVNC at <ip>:6080/vnc.html"
                → wait until current URL host == takeout.google.com
                  (no timeout)
                → resume download flow (jump back to A)
```

Tmp-dir-then-rename means partial downloads never leak into the final
exports dir even if the container is killed mid-download. On startup, the
downloader empties the tmp dir before starting; orphaned partials from
prior runs are discarded.

## Error handling

| Scenario | Behavior |
|---|---|
| chromedp fails to spawn (chrome path wrong, OOM) | Return error from `Download`. Caller's `os.Exit(3)` path fires `error` notify. `pending_export.uuid` stays put → next restart retries. |
| Cookie injection fails | Same as above — error up, marker stays, retry on restart. |
| Navigation error (network down, DNS, 5xx) | Per-URL retry with simple backoff (3 tries, 30s/2m/5m). After 3 fails, error up. |
| Challenge appears | Headed mode + `auth_expired` notify, block forever waiting for URL to leave challenge page. No timeout — Google's URL stays valid until the export expires (~7 days), and the user has noVNC + pushover. |
| Download stalls (no progress for 5 min) | Treat as broken: cancel current navigation, error up. Marker stays, restart resumes. |
| Download completes but magic-bytes check fails | Delete the file, error up. Means Google's response was bogus — almost certainly HTML masquerading as zip. Rare with chromedp but worth catching. |
| Container killed mid-download | Tmp file orphaned in `downloads-tmp/`. On next start, downloader empties tmp dir before starting. Marker still present → re-fetches whole file from Google (resume across browser restarts isn't trivial; full re-fetch is acceptable for a 7-day URL window). |
| All downloads succeed | `clearPendingExport()` runs as today. Next cycle creates fresh export. |

## Testing

| Layer | Strategy |
|---|---|
| Pure helpers (magic-bytes check, tmp-dir setup, file move/rename, URL→filename derivation) | Unit tests with table-driven cases. No browser involved. |
| Browser bootstrap (cookie injection, setDownloadBehavior CDP call) | Skip — wrappers around well-tested chromedp + CDP; tests would just verify chromedp itself works. |
| Per-URL download loop | Integration test using `httptest.Server` that serves a tiny real ZIP with proper headers. chromedp navigates to it, verify the file appears in the output dir with correct content. Tagged `//go:build integration` so CI can skip if Chrome isn't available. |
| Challenge detection | Same `httptest.Server` — return a 302 to a fake "sign-in" path, verify the detector triggers, fires the notify hook (captured by stub), and unblocks once we redirect back. Same integration tag. |
| End-to-end against real Google | Manual: `gxodus debug-download <uuid>` (new helper command) skips create+poll and exercises the download path against a known UUID. User runs in the container, verifies real >GB archives. |

## Pushover integration

The existing notify system fires user-configured shell commands per event
(`on_auth_expired`, `on_export_complete`, etc.). To meet the user's stated
need for Pushover-on-challenge without forcing them to assemble a curl
command, add a built-in Pushover destination as a parallel channel.

**Config:**

```toml
[notify.pushover]
token    = "<app token>"
user_key = "<user key>"
# events = ["auth_expired", "export_complete", "error"]   # default
```

If `token` and `user_key` are both set, gxodus posts to
`https://api.pushover.net/1/messages.json` (form-encoded) for every event
listed in `events`. `events` defaults to the list above — `export_started`
is opt-in only because it's noisy on a 180-day cycle. Messages are
hard-coded in v1 (no user-configurable templates); the bracketed
substitutions below are filled by the code from `EventData`/runtime:

| Event | Title | Message |
|---|---|---|
| `auth_expired` | gxodus: re-auth needed | `Open noVNC at {host}:6080/vnc.html and complete the password challenge.` |
| `export_complete` | gxodus: export ready | `Downloaded {size} to {path}.` |
| `error` | gxodus: error | `{error}` |
| `export_started` | gxodus: export started | `New Takeout submitted (uuid={uuid}).` |

`{host}` resolves to `os.Hostname()` by default, overridable via
`GXODUS_PUBLIC_HOSTNAME` env var (useful when the container hostname
differs from the LAN address the user types into a browser).

**Code shape:**

- New file `internal/notify/pushover.go` — single function
  `sendPushover(cfg PushoverConfig, title, message string) error` that
  POSTs to the Pushover API with a 10s timeout. No retries; Pushover's
  reliability is on Pushover.
- `notify.Fire` extended: after the existing shell-hook dispatch, if
  `cfg.Pushover.Token != ""` and the event is in `cfg.Pushover.Events`,
  call `sendPushover` with the baked-in title/message for that event.
  Errors logged to stderr, never propagated (notifications must not
  block exports).
- `internal/config/config.go` gains `PushoverConfig` struct and the
  `events` field. Defaults applied in `config.Load`.

Pushover and shell hooks coexist independently — users can have either,
both, or neither. No deprecation or migration of `on_*` keys.

## Out of scope

- **Concurrent / parallel downloads.** Sequential is simpler, less likely
  to trip Google rate limits, and the multi-hour wall-clock for a 4.46 GB
  export is dominated by Google's bandwidth allocation, not our request
  pattern.
- **Resume across browser restarts.** Re-fetching the whole archive on
  restart is acceptable inside the 7-day URL window. CDP-level resume is
  intricate and adds correctness risk.
- **Drive-delivery alternative.** Researched and rejected: counts against
  user's Drive quota, fails for 550 GB exports.
- **Removing chromium from the runtime image.** Drive delivery would have
  enabled this, but with chromedp downloads we keep chromium anyway.

## Migration

The existing HTTP downloader code is deleted (the bogus `.zip`-named HTML
files it produced have already burned us once and the magic-bytes guard
won't help if Google ever returns a 200 with a "real" but unauthenticated
fallback body). Caller signature change is minor (`Download` gains a
`notify.Config` parameter). No data-format change; output paths are
identical.
