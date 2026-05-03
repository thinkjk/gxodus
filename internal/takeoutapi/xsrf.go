package takeoutapi

import (
	"fmt"
	"regexp"
)

// PageTokens are the per-page secrets needed to call batchexecute.
type PageTokens struct {
	XSRF       string // the "at" parameter
	BuildLabel string // the "bl" parameter (rotates with Google deploys)
}

// SNlM0e = XSRF; cfb2h = build label.
// We regex these out instead of parsing JS — the surrounding object can have
// any number of trailing keys with arbitrary nesting.
var (
	xsrfRE  = regexp.MustCompile(`"SNlM0e":"([^"]+)"`)
	buildRE = regexp.MustCompile(`"cfb2h":"([^"]+)"`)
)

// extractTokens parses the takeout.google.com page HTML for the XSRF token
// and build label. Both live in the WIZ_global_data global object embedded
// in the page.
func extractTokens(html string) (*PageTokens, error) {
	xsrf := xsrfRE.FindStringSubmatch(html)
	if len(xsrf) < 2 {
		return nil, fmt.Errorf("XSRF token (SNlM0e) not found in page HTML")
	}
	build := buildRE.FindStringSubmatch(html)
	if len(build) < 2 {
		return nil, fmt.Errorf("build label (cfb2h) not found in page HTML")
	}
	return &PageTokens{
		XSRF:       xsrf[1],
		BuildLabel: build[1],
	}, nil
}
