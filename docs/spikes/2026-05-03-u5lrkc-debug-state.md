# U5lrKc Create-Export Debug — RESOLVED 2026-05-04

## Status: fixed pending live verification

Captured a working create from a real browser via Playwright MCP and diffed
against gxodus's request. Root cause was the args shape — we were sending the
inner positional args flat alongside `"ac.t.st"`, but the real request wraps
them in a sub-array.

## Root cause

**Wrong (gxodus before fix):**

```json
["ac.t.st", [["drive"]], "ZIP", null, 5, null, 2147483648, 1, null, null, null, "2"]
```

**Correct (captured browser request):**

```json
["ac.t.st", [[["drive"]], "ZIP", null, 5, null, 2147483648, 1, null, null, null, "0"]]
```

Two-element outer array — action name and a single inner array — not a flat
12-element list. The flat form returns `error code 3` (INVALID_ARGUMENT).

The original 2026-05-02 spike doc documented the args structure as flat —
that was a misread. Fixed in `internal/takeoutapi/exports.go`
(`buildCreateExportArgs`). Test `TestClient_CreateExport_PayloadShape` now
asserts the exact captured shape so this can't regress.

Trailing constant also changed from `"2"` (guessed) to `"0"` (captured).

## Other deltas the browser definitively does not send

While diffing, we also observed the real browser create request does NOT send:

- `pageId=none` URL parameter — removed.
- `Authorization: SAPISIDHASH ...` header — removed (along with the entire
  `buildSAPISIDHashAuth` helper). Cookies alone are sufficient for both reads
  and writes; the SAPISIDHASH spike from earlier was a dead end.

URL params still sent (matched against capture): `rpcids`, `source-path`,
`f.sid`, `bl`, `hl=en`, `soc-app=1`, `soc-platform=1`, `soc-device=1`,
`_reqid`, `rt=c`. Headers still sent: `content-type`, `x-same-domain`,
`origin`, `user-agent`, `accept`, `accept-language`,
`x-goog-ext-525002608-jspb: [215]`, `referer`.

## Captured request (full evidence)

URL:

```
POST https://takeout.google.com/_/TakeoutUi/data/batchexecute
  ?rpcids=U5lrKc
  &source-path=%2F
  &f.sid=-6704026795126851156
  &bl=boq_identityfrontenduiserver_20260429.06_p0
  &hl=en
  &soc-app=1
  &soc-platform=1
  &soc-device=1
  &_reqid=670569
  &rt=c
```

Body (URL-decoded):

```
f.req=[[["U5lrKc","[\"ac.t.st\",[[[\"drive\"]],\"ZIP\",null,5,null,2147483648,1,null,null,null,\"0\"]]",null,"generic"]]]
&at=ALYeEnl--CbSOt25T1eL-pkUJz5Y:1777862167173
```

Response (success — 200, single `wrb.fr` chunk):

```json
["ac.t.star",
  ["ac.t.ta",
   "abc8cb7e-4c9c-40c3-aef7-f1219b8bf946",   // new export UUID
   "May 3, 2026",
   null, "", null, 0,
   [["drive","Drive","https://www.gstatic.com/.../drive_2020q4_32dp.png",
     null,null,null,null,null,
     "https://www.gstatic.com/.../drive_2020q4_64dp.png"]],
   null, 0, null, false, null, null, null, null, null, null,
   5,                                          // frequency code echoed back
   null, null, false,
   1777862268368,                              // creation timestamp (ms)
   null, 0, null, 1, null, null, true]]
```

So `U5lrKc` returns the new export UUID at inner-array position [1] —
`scrapeUUID` already finds this. The action name flips from `ac.t.st`
(submit) on request to `ac.t.star` (?) on response.

## Files changed

- `internal/takeoutapi/exports.go` — `buildCreateExportArgs` now nests inner args.
- `internal/takeoutapi/client.go` — drop `pageId` URL param, drop
  `Authorization` header, delete `buildSAPISIDHashAuth` + `cookieValue`,
  drop `crypto/sha1` and `encoding/hex` imports.
- `internal/takeoutapi/exports_test.go` — `TestClient_CreateExport_PayloadShape`
  now asserts exact captured shape (was loose `Contains` checks).

## Verification plan

1. Build and push container image.
2. Run `gxodus debug-create --products drive` against a fresh account.
3. Expect: rpc returns success, response contains a UUID, then
   `gxodus debug-list` shows the new in-progress export.
