package takeoutapi

import (
	"fmt"
	"regexp"
)

// PageTokens are the per-page secrets needed to call batchexecute.
type PageTokens struct {
	XSRF       string // the "at" parameter
	BuildLabel string // the "bl" URL parameter (rotates with Google deploys)
	SessionID  string // the "f.sid" URL parameter; required by write rpcs (U5lrKc)
}

// SNlM0e = XSRF; cfb2h = build label; FdrFJe = session ID (f.sid).
// We regex these out instead of parsing JS — the surrounding object can have
// any number of trailing keys with arbitrary nesting.
var (
	xsrfRE      = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	buildRE     = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
	sessionIDRE = regexp.MustCompile(`"FdrFJe":"(-?\d+)"`)
)

// extractTokens parses the takeout.google.com page HTML for the XSRF token,
// build label, and session ID. All three live in the WIZ_global_data global
// object embedded in the page. SessionID is optional (read rpcs work without
// it) but required for write rpcs like U5lrKc.
func extractTokens(html string) (*PageTokens, error) {
	xsrf := xsrfRE.FindStringSubmatch(html)
	if len(xsrf) < 2 {
		return nil, fmt.Errorf("XSRF token (SNlM0e) not found in page HTML")
	}
	build := buildRE.FindStringSubmatch(html)
	if len(build) < 2 {
		return nil, fmt.Errorf("build label (cfb2h) not found in page HTML")
	}
	tokens := &PageTokens{
		XSRF:       xsrf[1],
		BuildLabel: build[1],
	}
	// Session ID is optional — log absence but don't fail.
	if sid := sessionIDRE.FindStringSubmatch(html); len(sid) >= 2 {
		tokens.SessionID = sid[1]
	}
	return tokens, nil
}
