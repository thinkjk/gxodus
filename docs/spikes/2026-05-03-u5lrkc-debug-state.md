# U5lrKc Create-Export Debugging State (2026-05-03)

> **For the next session:** Read this top-to-bottom before doing anything. We're mid-investigation; everything we've ruled out is here so you don't repeat work.

## TL;DR

- gxodus has been fully rewritten from chromedp DOM-scraping to HTTP calls against `takeout.google.com/_/TakeoutUi/data/batchexecute`. Reads work end-to-end (list, status, download URLs all validated against real data).
- The only broken piece: **`U5lrKc` (create export) returns `error code 3` (INVALID_ARGUMENT) with no message**, on every account, every payload variation. Reads (`fhjYTc`) work fine with the same client + cookies + headers.
- We've exhausted every blind-shot fix. Next step: capture a real working U5lrKc from a browser via **Playwright MCP** (just installed) and diff it against ours.

## Where to resume

User installed Playwright MCP for me to drive a real Chromium and capture the request:

```bash
claude mcp add playwright -- npx -y @playwright/mcp@latest --headed --user-data-dir=$HOME/.cache/playwright-mcp-profile
```

Plan:
1. Use Playwright MCP to open `accounts.google.com` so the user logs in (one time — profile persists).
2. Navigate to `https://takeout.google.com/`.
3. Step through the wizard (select products → Next step → set options → Create export).
4. Use `browser_network_requests` to grab the `U5lrKc` POST — capture URL, headers, body.
5. Diff vs. our request (request body is dumped to `/config/debug/rpc-U5lrKc-request-*.html` on every gxodus call).
6. Patch the difference, push, deploy, test.

## What we've ruled out (do NOT re-test)

| Hypothesis | Result | Evidence |
|---|---|---|
| Cookie auth (general) | ✓ works | fhjYTc reads succeed |
| Cookie auth (filter/dedup) | ✓ correct | 40 → 18 cookies, no CookieMismatch |
| XSRF token (`SNlM0e` / `at`) | ✓ extracted, sent in body | 42-char token, fresh per call |
| Build label (`cfb2h` / `bl`) | ✓ correct | `boq_identityfrontenduiserver_*` confirmed by spike doc |
| Session ID (`FdrFJe` / `f.sid`) | ✓ extracted, sent in URL | 64-bit signed integer present |
| Headers: Origin, Referer, X-Same-Domain, x-goog-ext-525002608-jspb | ✓ all set | per spike + browser captures |
| **SAPISIDHASH/1PHASH/3PHASH Authorization** | ✓ all 3 sent, **didn't fix it** | computed from SAPISID-family cookies |
| Multi-block WIZ_global_data picker | ✓ identity block is correct | spike confirms; takeoutui block doesn't exist on the page |
| Account-has-existing-export conflict | ✗ not the issue | tested on fresh account with 0 exports |
| Product list invalid | ✗ not the issue | fails with single `["drive"]` too |
| Positional args bisect (freq=0/1/5, trailing="" /1/2, flag=0/1, format=ZIP/TGZ) | ✗ not the issue | every variation returned same code 3 |

## Current code state

- Branch: `main` (working directly on main per user preference)
- Last commit: `bdf90ac` "Add SAPISIDHASH auth + debug-tokens/list/create commands"
- Image: `ghcr.io/thinkjk/gxodus:main` / `sha-bdf90ac` pushed to ghcr
- Build: clean, `go test ./...` passes

## Useful debug commands (already in the binary)

Run inside the container as `gxodus <cmd>`:

| Command | Purpose |
|---|---|
| `gxodus debug-tokens` | Fetch takeout page, dump XSRF/bl/sid/cookies, no rpc call |
| `gxodus debug-list` | Call fhjYTc, pretty-print exports |
| `gxodus debug-create --products drive` | Call U5lrKc with simple flags (instead of constructing JSON) |
| `gxodus debug-create --freq N --flag N --trailing X --format ZIP/TGZ --size BYTES` | Vary individual positional args |
| `gxodus debug-api --rpcid X --args '[...]' --version generic` | Raw escape hatch for any rpcid |

Every rpc call also dumps:
- `/config/debug/takeout-page-*.html` — page HTML (with WIZ_global_data)
- `/config/debug/rpc-X-request-*.html` — exact URL-encoded request body sent
- `/config/debug/rpc-X-error-N-*.html` — full response body on any error chunk

## Files that matter for this debug

- `internal/takeoutapi/client.go` — `CallRPC` (lines 251-372ish), `buildSAPISIDHashAuth` (bottom), `ensureTokens` (~line 150)
- `internal/takeoutapi/exports.go` — `buildCreateExportArgs` (the U5lrKc payload structure)
- `internal/takeoutapi/xsrf.go` — token extraction from page HTML
- `internal/cli/debug_api.go` — all the debug-* commands
- `docs/spikes/2026-05-02-batchexecute-api.md` — protocol notes; spike doc explicitly says U5lrKc response was **never captured** (this is exactly why we're stuck)

## What gxodus IS using right now

- Go 1.26, cobra, standard library only for `takeoutapi` (no chromedp at runtime)
- chromedp v0.15.1 still present for the **auth flow only** (one-time browser login via noVNC, then session cookies are reused for all API calls)
- Docker container with optional noVNC for ad-hoc re-auth
- Polls via API (`fhjYTc`) — no chromium spawn per cycle anymore

## Account context

- The account currently authenticated in the gxodus container is a fresh test account with **0 exports** (per `debug-list` output).
- A different account (used earlier in conversation) has completed exports and was used to validate the download URL formula.
- User said: "I can only create so many requests" — be respectful of Takeout rate limits when capturing.

## Once Playwright capture lands

What we need to extract from the captured U5lrKc request:
- Full request URL (especially any URL params we're missing)
- All request headers (especially anything we don't already send: `x-goog-batchexecute-bgr`?, `Authorization`?, custom `x-*` headers?)
- Full request body (the URL-encoded form data — compare `f.req` JSON byte-for-byte with our dumped `/config/debug/rpc-U5lrKc-request-*.html`)
- The response (so we finally know the success-shape too — handy for `CreateExport` to parse the new export's UUID directly)

Then patch `internal/takeoutapi/client.go` and/or `internal/takeoutapi/exports.go` to match. Build, push, restart, re-run `gxodus debug-create --products drive` — should succeed.
