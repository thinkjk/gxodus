// Package takeoutapi wraps Google Takeout's internal batchexecute endpoint.
// See docs/spikes/2026-05-02-batchexecute-api.md for protocol notes.
package takeoutapi

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// encodeRequest builds the URL-encoded body for a batchexecute POST.
// args is a pre-serialized JSON string (the "doubly-escaped" inner payload —
// it gets serialized again as a JSON string when the envelope is marshaled).
// version is "generic" or "1" depending on the rpcid.
// atToken is the XSRF token from the page HTML.
func encodeRequest(rpcid, args, version, atToken string) (string, error) {
	envelope := [][][]interface{}{{{rpcid, args, nil, version}}}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshaling envelope: %w", err)
	}

	values := url.Values{}
	values.Set("f.req", string(encoded))
	values.Set("at", atToken)
	return values.Encode(), nil
}
