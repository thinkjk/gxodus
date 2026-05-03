// Package takeoutapi wraps Google Takeout's internal batchexecute endpoint.
// See docs/spikes/2026-05-02-batchexecute-api.md for protocol notes.
package takeoutapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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

// rpcResult is one decoded rpc invocation from a batchexecute response.
type rpcResult struct {
	RpcID   string
	RawJSON []byte // the inner JSON-string parsed back out (the "doubly-escaped" payload)
}

const antiHijackPrefix = ")]}'\n"

// decodeResponse parses Google's chunked batchexecute response body.
// Returns one rpcResult per "wrb.fr"-tagged chunk in the response.
func decodeResponse(body []byte) ([]rpcResult, error) {
	if !bytes.HasPrefix(body, []byte(antiHijackPrefix)) {
		return nil, errors.New("response missing )]}' prefix — not a batchexecute body")
	}
	body = bytes.TrimPrefix(body, []byte(antiHijackPrefix))

	var results []rpcResult
	for len(body) > 0 {
		// Each chunk: <decimal length>\n<JSON bytes>
		nl := bytes.IndexByte(body, '\n')
		if nl <= 0 {
			break
		}
		lengthStr := strings.TrimSpace(string(body[:nl]))
		if lengthStr == "" {
			body = body[nl+1:]
			continue
		}
		chunkLen, err := strconv.Atoi(lengthStr)
		if err != nil {
			return nil, fmt.Errorf("parsing chunk length %q: %w", lengthStr, err)
		}
		body = body[nl+1:]
		if len(body) < chunkLen {
			return nil, fmt.Errorf("chunk says %d bytes but only %d remain", chunkLen, len(body))
		}
		chunkJSON := body[:chunkLen]
		body = body[chunkLen:]
		// Skip the trailing newline after the chunk (if present)
		if len(body) > 0 && body[0] == '\n' {
			body = body[1:]
		}

		// Each chunk is a JSON array of arrays; the rpc results are the elements
		// whose first item is the literal string "wrb.fr".
		var chunkArr [][]interface{}
		if err := json.Unmarshal(chunkJSON, &chunkArr); err != nil {
			// Some chunks (the "di"/"e" diagnostic ones) won't match this shape — skip.
			continue
		}
		for _, entry := range chunkArr {
			if len(entry) < 3 {
				continue
			}
			tag, ok := entry[0].(string)
			if !ok || tag != "wrb.fr" {
				continue
			}
			rpcid, ok := entry[1].(string)
			if !ok {
				continue
			}
			inner, ok := entry[2].(string)
			if !ok {
				continue
			}
			results = append(results, rpcResult{
				RpcID:   rpcid,
				RawJSON: []byte(inner),
			})
		}
	}
	return results, nil
}
