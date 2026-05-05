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
a fresh challenge. When a challenge does appear, the browser switches to
headed mode (visible via noVNC), fires the `auth_expired` notify hook
(reusing the user's Pushover setup), and blocks indefinitely until the
URL leaves the challenge page.

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
   and `Headless=true`. Inject the saved session cookies. Configure CDP
   `Browser.setDownloadBehavior` to drop files into a known temp dir at
   `$CONFIG_DIR/downloads-tmp/`. Empty that dir first so abandoned partials
   from a previous container don't confuse us.

2. **Per-URL download loop.** For each URL: navigate, listen for CDP
   `Browser.downloadProgress` events. On `downloadWillBegin` log the
   filename + total bytes. On `state="completed"` move the file from the
   tmp dir to the final output dir via `os.Rename` (atomic within the same
   filesystem). Run the magic-bytes check on the final file as a defense-in-
   depth sanity check.

3. **Challenge detector.** After navigating, race three outcomes for ~10s:
   (a) `downloadWillBegin` event — happy path; (b) URL still off
   `takeout.google.com` — challenge; (c) navigation error — bubble up. On
   challenge: switch the chromedp context to headed mode (so noVNC shows
   it), fire `auth_expired` notify, log the noVNC URL, then block on
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
  ├── browser.NewContext(headless=true, UserDataDir=ProfileDir())
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
       └── if B → switch ctx to headed mode (so noVNC shows it)
                → notify.Fire("auth_expired", {...})
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
