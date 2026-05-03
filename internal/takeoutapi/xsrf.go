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
// A Takeout page can embed multiple `WIZ_global_data` blocks — typically one
// for an identity widget header and one for the actual TakeoutUi product.
// Each block has its own SNlM0e / cfb2h / FdrFJe, and they are NOT
// interchangeable: posting to TakeoutUi's batchexecute with the identity
// frontend's XSRF + bl yields error code 3.
//
// We split the HTML into per-block windows and pick the one whose `cfb2h`
// build label looks like a takeout build (`boq_takeoutuiserver_*`). If no
// block matches, we fall back to the first block.
func extractTokens(html string) (*PageTokens, error) {
	blocks := splitWizBlocks(html)
	if len(blocks) == 0 {
		// No WIZ_global_data anchor found — try the whole document as one block.
		blocks = []string{html}
	}

	// Prefer the block whose build label starts with boq_takeoutuiserver.
	var chosen string
	for _, b := range blocks {
		if m := buildRE.FindStringSubmatch(b); len(m) >= 2 && strings.HasPrefix(m[1], "boq_takeoutuiserver") {
			chosen = b
			break
		}
	}
	if chosen == "" {
		// Fallback: first block that has both required tokens.
		for _, b := range blocks {
			if buildRE.MatchString(b) && xsrfRE.MatchString(b) {
				chosen = b
				break
			}
		}
	}
	if chosen == "" {
		return nil, fmt.Errorf("no WIZ_global_data block with SNlM0e + cfb2h found")
	}

	xsrf := xsrfRE.FindStringSubmatch(chosen)
	if len(xsrf) < 2 {
		return nil, fmt.Errorf("XSRF token (SNlM0e) not found in chosen block")
	}
	build := buildRE.FindStringSubmatch(chosen)
	if len(build) < 2 {
		return nil, fmt.Errorf("build label (cfb2h) not found in chosen block")
	}

	tokens := &PageTokens{
		XSRF:       xsrf[1],
		BuildLabel: build[1],
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
