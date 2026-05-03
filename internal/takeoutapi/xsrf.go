package takeoutapi

import (
	"fmt"
	"regexp"
	"strings"
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
// build label, and session ID.
//
// The takeout page can embed multiple WIZ_global_data blocks. Confirmed via
// 2026-05-02 captures: the **identity** frontend's block carries the tokens
// the TakeoutUi batchexecute endpoint expects (`bl=boq_identityfrontenduiserver_*`).
// We pick the first block that has both an XSRF token and a build label.
func extractTokens(html string) (*PageTokens, error) {
	blocks := splitWizBlocks(html)
	if len(blocks) == 0 {
		blocks = []string{html}
	}

	var chosen string
	for _, b := range blocks {
		if buildRE.MatchString(b) && xsrfRE.MatchString(b) {
			chosen = b
			break
		}
	}
	if chosen == "" {
		return nil, fmt.Errorf("no WIZ_global_data block with SNlM0e + cfb2h found")
	}

	tokens := &PageTokens{
		XSRF:       xsrfRE.FindStringSubmatch(chosen)[1],
		BuildLabel: buildRE.FindStringSubmatch(chosen)[1],
	}
	if sid := sessionIDRE.FindStringSubmatch(chosen); len(sid) >= 2 {
		tokens.SessionID = sid[1]
	}
	return tokens, nil
}

// splitWizBlocks returns the HTML cut into per-WIZ_global_data windows.
// Each window starts at one `WIZ_global_data` occurrence and ends just before
// the next, so per-block regex extraction can't bleed into a sibling block.
func splitWizBlocks(html string) []string {
	const marker = "WIZ_global_data"
	var starts []int
	for i := 0; ; {
		j := strings.Index(html[i:], marker)
		if j < 0 {
			break
		}
		starts = append(starts, i+j)
		i += j + len(marker)
	}
	if len(starts) == 0 {
		return nil
	}
	out := make([]string, 0, len(starts))
	for k, s := range starts {
		end := len(html)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		out = append(out, html[s:end])
	}
	return out
}
