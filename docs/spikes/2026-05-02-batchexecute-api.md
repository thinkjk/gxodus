# Spike: Replace chromedp with Google Takeout's `batchexecute` API

**Date:** 2026-05-02
**Status:** Investigation complete, implementation deferred
**Outcome:** API is reachable and usable with session cookies; replacement would take 1-2 weeks part-time work + carry rpcid-rotation risk.

## Why this spike happened

gxodus drives the Takeout web UI via chromedp + DOM scraping. That's brittle — Google rotates Material Design class names, bot detection blocks too-clean automation, and the whole flow needs a chromium running on Xvfb in a Docker container.

If Google's web UI calls a JSON-ish HTTP endpoint behind the scenes that we could call directly with the session cookies we already extract, we'd replace ~600 lines of chromedp DOM scraping with ~200 lines of HTTP calls. No chromium for export/poll, only for the initial login.

## What we tried and ruled out

- **`takeout-pa.googleapis.com`** (the proper Google Cloud-style API). Reverse-engineered discovery doc exists ([gist](https://gist.github.com/stewartmcgown/7f5dcbf4ccd385637786f9581b620e6a), [OpenAPI v3](https://gist.github.com/AlexDev404/4041742c1c710ab2a0c3df1c62df6137)). With a real OAuth token (`drive.readonly` scope) the endpoint returns:

  ```json
  {
    "code": 403,
    "status": "PERMISSION_DENIED",
    "details": [{ "reason": "SERVICE_DISABLED", "service": "takeout-pa.googleapis.com" }]
  }
  ```

  The API is real and hosted, but it can't be enabled in a normal user's Google Cloud Console — the Cloud Console search for "Takeout API" returns nothing. AlexDev404 confirmed it worked in 2022 but no recent confirmations. Effectively dead for personal-account use.

- **Google Drive API "create takeout"** — no such method exists in the Drive REST API.
- **Workspace Admin SDK / Vault API** — different products, admin-only or different scope.
- **Data Portability API** (https://developers.google.com/data-portability) — official, OAuth-based, currently EEA-focused. Worth a separate evaluation if it covers Takeout-style backups, but doesn't match today's flow.

## What we found that DOES work

The Takeout web UI calls `https://takeout.google.com/u/{N}/_/TakeoutUi/data/batchexecute` with session cookies. **No Google Cloud project enablement needed** — it uses the user's session cookies (NID, SID, SAPISID, etc.) like any other Google web UI call. We already extract those cookies in `gxodus auth`.

### Endpoint format

```
POST https://takeout.google.com/u/{userIdx}/_/TakeoutUi/data/batchexecute
  ?rpcids={rpcid}
  &source-path=%2Fu%2F{userIdx}%2F[manage]
  &f.sid={sessionId}             # 64-bit signed integer, persistent for the page session
  &bl=boq_identityfrontenduiserver_{date}.06_p0   # build label, rotates with Google deploys
  &hl=en
  &pageId=none
  &soc-app=1&soc-platform=1&soc-device=1
  &_reqid={incrementingCounter}  # client-side counter for ordering
  &rt=c                          # "compact" return type

Headers:
  Cookie: <full session, including SAPISID for SAPISIDHASH auth>
  Content-Type: application/x-www-form-urlencoded;charset=utf-8
  Origin: https://takeout.google.com
  Referer: https://takeout.google.com/u/{N}/...
  X-Same-Domain: 1

Body (URL-encoded):
  f.req=<JSON>&at=<XSRF token>
```

### `f.req` structure

The body is a deeply-nested JSON array (Google's "batchexecute envelope"):

```json
[[[ "<rpcid>", "<args as JSON-string>", null, "<version>" ]]]
```

The args are a JSON string (escaped) inside the outer JSON. Version is `"generic"` or `"1"` depending on the call.

The `at` parameter is an XSRF token that comes from the page's initial HTML (look for `WIZ_global_data` or similar). It looks like `ALYeEnkc1UxeQ3U_BuS-1yJoUbY8:1777768009152` — a base64-ish prefix and a timestamp.

### Confirmed rpcids (from 2026-05-02 captures)

| rpcid | Page | Action | Args (decoded) | Version | Notes |
|---|---|---|---|---|---|
| `U5lrKc` | `/u/N/?pageId=none` | **Create export** | See below | `"generic"` | The big one. Action name `ac.t.st`. |
| `RN3tcc` | `/u/N/?pageId=none` | List exports (?) | `[]` | `"1"` | Fires on Page 1 navigation. Empty args. |
| `fhjYTc` | `/u/N/manage` | List exports (?) | `[]` | `"generic"` | Fires on the manage page. Empty args. |
| `OIek4b` | `/u/N/?pageId=none` | Status / cancel (?) | `[["{export-uuid}"]]` | `"generic"` | Takes an export UUID. Probably "get status by ID" or "cancel". |
| `jODH4c` | All pages | Heartbeat | `["ac.t.saq", "{session-uuid}"]` | `"generic"` | `ac.t.saq` = "stay alive query". Fires every ~2 sec. **Ignore for any real flow.** |

### `U5lrKc` create-export args, decoded

```json
[
  "ac.t.st",                            // action name — "takeout submit"
  [                                      // 2: list of selected products
    ["alerts"], ["analytics"], ["android"],
    ["arts_and_culture"], ["course_kit"], ["blogger"],
    ... ~80 entries, each is [product_slug] ...
    ["bond"]
  ],
  "ZIP",                                 // 3: archive format. "ZIP" | "TGZ"
  null,                                  // 4: ?
  5,                                     // 5: frequency code (5 = ?? — needs a TGZ/2-month capture to confirm)
  null,                                  // 6: ?
  53687091200,                           // 7: file size in BYTES (50 GB = 50 * 1024^3)
  1,                                     // 8: flag (split? compression level?)
  null, null, null,                      // 9-11: ?
  "2"                                    // 12: schedule code? version?
]
```

#### Known product slugs (from an actual capture; not exhaustive)

`alerts`, `analytics`, `android`, `arts_and_culture`, `course_kit`, `blogger`, `brand_accounts`, `calendar`, `chrome`, `chrome_os`, `chrome_web_store`, `classroom`, `contacts`, `discover`, `drive`, `family`, `fiber`, `fit`, `fitbit`, `ai_sandbox`, `gemini`, `google_account`, `google_ads`, `my_business`, `hangouts_chat`, `google_cloud_search`, `developer_platform`, `earth`, `feedback`, `google_finance`, `support_content`, `meet`, `google_one`, `google_pay`, `photos`, `books`, `play_games_services`, `play_movies`, `play`, `podcasts`, `hats_surveys`, `shopping`, `google_store`, `google_wallet`, `apps_marketplace`, `groups`, `home_graph`, `keep`, `gmail`, `manufacturer_center`, `maps`, `local_actions`, `merchant_center`, `messages`, `my_activity`, `nest`, `news`, `package_tracking`, `search_console`, `personal_safety`, `assisted_calling`, `backlight`, `pixel_telemetry`, `profile`, `custom_search`, `my_orders`, `reminders`, `save`, `search_ugc`, `search_notifications`, `streetview`, `tasks`, `location_history`, `voice`, `voice_and_audio_activity`, `workflows`, `bond`

Note: `access_log_activity` is NOT the slug. **The slug is `bond`** (confirmed via the `fhjYTc` response — see below). Both `bond` and `gemini` (and a few others labeled "NOT_SET") appear in the catalog without obvious icon assets, suggesting they're slugs Google added but hasn't fully wired up to all UI surfaces. To include Activity Logs in a `U5lrKc` create call, append `["bond"]` to the products array.

### Response format (general)

Google's batchexecute responses are a custom chunked format described in [Ryan Kovatch's "Deciphering batchexecute" guide](https://kovatch.medium.com/deciphering-google-batchexecute-74991e4e446c). Always start with `)]}'\n` (anti-JSON-hijacking prefix), then **length-prefixed chunks**: a decimal byte count on its own line, then the JSON array of that many bytes, repeated until end of body.

```
)]}'
30284
[["wrb.fr","fhjYTc","<doubly-escaped JSON-string of the result>",null,null,null,"generic"]]
57
[["di",123],["af.httprm",122,"-8797266961199462245",9]]
27
[["e",4,null,null,30387]]
```

The first chunk is the actual response wrapped in `["wrb.fr", rpcid, <JSON-string-of-result>, ...]`. Subsequent chunks are diagnostic / framing. The result-JSON-string is doubly-escaped — parse the outer JSON, then `JSON.parse()` the result string to get the actual payload.

The Python library [pndurette/pybatchexecute](https://github.com/pndurette/pybatchexecute) is the most-mature reference implementation for parsing.

### `fhjYTc` (list exports) response — decoded

Captured 2026-05-02 with one in-progress export in the user's account:

```json
[null,
  [[null, [
    "ac.t.ta",                                    // [0] action — "takeout active" (presumed)
    "0dc01143-391b-480f-8574-3e40c7c1e43f",       // [1] EXPORT UUID (this is the same UUID the heartbeat carries)
    "May 2, 2026",                                // [2] creation date display
    null,                                          // [3] ?
    "",                                            // [4] ?
    null,                                          // [5] ?
    0,                                             // [6] ? possibly archive count
    [ /* product catalog: 80 entries */ ],         // [7] all available products w/ metadata
    null,                                          // [8] ?
    0,                                             // [9] status code? 0 = in_progress (presumed)
    null,                                          // [10] ?
    false,                                         // [11] ?
    null,                                          // [12] ?
    null,                                          // [13] ?
    ["May 2, 2026", "5:27 PM", "104.2.75.91"],    // [14] [date, time, originating-IP]
    null, null, null,                              // [15-17] ?
    5,                                             // [18] ?
    null, null,                                    // [19-20] ?
    false,                                         // [21] ?
    1777768027572,                                 // [22] creation Unix timestamp (ms)
    null,                                          // [23] ?
    0,                                             // [24] ?
    null,                                          // [25] ?
    1,                                             // [26] ?
    null,                                          // [27] ?
    [null, 0, true],                               // [28] sub-struct ?
    true                                           // [29] ?
  ]]],
  null,                                            // outer [2] ?
  "114106906800892523426",                         // outer [3] Google user ID
  false,                                           // outer [4] ?
  [ /* same single export duplicated as a flat array */ ]  // outer [5] ?
]
```

The product catalog at index [7] of the inner array is itself an array of:

```
[slug, displayName, iconUrl1x, null, 0, null, false, null, iconUrl2x]
```

#### Critical findings from the response

- **The "Access Log Activity" slug is `bond`, NOT `access_log_activity`.** Captured row: `["bond", "Access Log Activity", ...]`. This is why our `access_log_activity` guess didn't appear in the create-export payload — the actual slug is `bond`.
- **The export UUID is the same as the heartbeat UUID.** Every `jODH4c` heartbeat carries `0dc01143-391b-480f-8574-3e40c7c1e43f` — the in-progress export's UUID. The "stay alive" call is **per-export**, not per-page-session.
- **No download URLs are present in the in-progress state.** Position [9] is `0` (likely status "in progress") and the `["May 2, 2026", "5:27 PM", "104.2.75.91"]` is creation, not completion. Once the export completes, we expect to see download URLs added somewhere in this structure (TBD: re-capture when the user's export finishes).
- **The originating IP is logged in the response.** Useful for confirming the request came from where you expect.
- **No completed exports** in this response — confirms the manage page hides in-progress in its UI but they DO surface in the API.

#### Status code hypothesis

Position [9] is currently `0` for the in-progress export. To confirm the enum, re-capture this response when:
- A completed export exists → see if [9] becomes `1` or some other value
- A failed export exists → ditto
- An export is older than 7 days (Google expires download links after a week)

### Other rpcid responses — still unknown

We did not capture responses for `U5lrKc` (create), `RN3tcc` (Page-1 list), or `OIek4b` (UUID-action). Capture those next session for full coverage.

## What's still unknown

To replace chromedp end-to-end, we still need to figure out:

1. **Response shape of `U5lrKc`** — does create-export return the new export's UUID? Status enum? Or just `{success: true}` and we have to list-then-find?
2. **Response shape of `RN3tcc`** — fires from Page 1 with empty args version `"1"`. May return a different/lighter shape than `fhjYTc`.
3. **Which list rpcid to use** — `RN3tcc` (`"1"`) or `fhjYTc` (`"generic"`). `fhjYTc` is fully decoded (above) and contains the export UUID + creation timestamp. Pick `fhjYTc` unless `RN3tcc` is meaningfully different.
4. **What `OIek4b` does** — given a UUID, is it `getStatus`, `cancel`, or `delete`?
5. **The status code enum** — position [9] in the export object is `0` while in-progress. Need a complete and a failed export to confirm enum values.
6. **The download-URL location** — when an export completes, where in the `fhjYTc` response do download URLs appear? Likely a populated array at one of the currently-`null` positions. Capture the same response after the user's pending export finishes.
7. **Frequency / flag enum values in `U5lrKc` create call** — what does `5` mean for frequency-position? `1` for 8th-position? `"2"` for 12th-position? Compare a TGZ + every-2-months capture to a ZIP + once capture to triangulate.
8. **The `at` (XSRF) token lifecycle** — captured from page HTML on the first page load (look for `WIZ_global_data.SNlM0e` or similar). Does it expire? Rotate per session? Per page load?

**The export from 2026-05-02 has UUID `0dc01143-391b-480f-8574-3e40c7c1e43f`.** When it finishes (Google says "hours or days"), recapture the `fhjYTc` response to find where download URLs appear and what status code completed exports use. That single re-capture closes most of the open questions.

## Proposed implementation outline (for a future session)

### New package: `internal/takeoutapi/`

```
internal/takeoutapi/
  client.go        # HTTP client, cookie auth, XSRF token handling
  batchexecute.go  # Envelope encoding/decoding (the [[[rpcid, args, null, ver]]] dance)
  exports.go       # Create / List / Status / Cancel methods
  types.go         # Export, Status, Product structs
  client_test.go   # Mocked HTTP via httptest
```

### Replace these chromedp call sites

| Current | New |
|---|---|
| `internal/browser/takeout.go:InitiateExport` | `takeoutapi.Client.CreateExport(ctx, opts)` |
| `internal/browser/takeout.go:CheckExportStatus` | `takeoutapi.Client.ListExports(ctx)` then filter for our UUID |
| `internal/poller/poller.go:checkOnce` | Same `ListExports` call, no chromium spawn per poll |

### Keep these chromedp call sites

| Why keep |
|---|
| `internal/browser/login.go:InteractiveLogin` — still need a real browser for login (Google bot detection blocks chromedp-driven login) |
| `internal/browser/browser.go:ExtractCookies` — same reason |

### Effort estimate

- **Optimistic** (responses come back clean, no surprises): 2-3 days of focused Go work
- **Realistic** (handle edge cases — multi-archive exports, expired exports, throttling, 2-step verification challenges): 1-2 weeks part-time
- **Risk:** Google can rotate any rpcid in a UI deploy. We'd need a "if rpcid call fails, fall back to chromedp" path or an automated rpcid-extraction step (parse the page's JS bundle).

## How to extract a fresh rpcid when Google rotates them

Each batchexecute rpcid is registered in the page's JS. To find a rotated rpcid:

1. Open Takeout page in browser, open DevTools.
2. View page source, search for the action name (e.g. `"ac.t.st"` for create).
3. Find the `_F_jsUrl` or similar JS bundle URL near it.
4. Open that JS, search for the action name — the rpcid is usually a 6-char string near it.

Or simpler: capture-and-replay via DevTools Network tab whenever something breaks.

## References

- [Ryan Kovatch - Deciphering Google's batchexecute System](https://kovatch.medium.com/deciphering-google-batchexecute-74991e4e446c)
- [pybatchexecute (Python ref impl)](https://github.com/pndurette/pybatchexecute)
- [stewartmcgown - Reverse-engineered Takeout API discovery doc](https://gist.github.com/stewartmcgown/7f5dcbf4ccd385637786f9581b620e6a)
- [AlexDev404 - Takeout OpenAPI v3 spec](https://gist.github.com/AlexDev404/4041742c1c710ab2a0c3df1c62df6137)
- [Google Data Portability API](https://developers.google.com/data-portability)

## Verdict for tonight

Chromedp-based gxodus is functional and deployed. Today's exposure of the batchexecute API is encouraging but not finished. **Defer the replacement** — file this doc as the breadcrumb and pick it up if/when the chromedp path becomes too painful to maintain.
