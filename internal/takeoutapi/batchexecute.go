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
	RpcID     string
	RawJSON   []byte // the inner JSON-string parsed back out (may be empty if error)
	ErrorCode int    // non-zero when Google returned an error chunk
}

const antiHijackPrefix = ")]}'"

// decodeResponse parses Google's chunked batchexecute response body.
// Format (no separators between segments):
//
//	)]}'<length-digits><chunk-bytes><length-digits><chunk-bytes>...
//
// Optional whitespace (\n, \r, \t, space) between segments is tolerated.
//
// Each <chunk-bytes> is a JSON array of arrays. Returns one rpcResult per
// "wrb.fr"-tagged entry. When a wrb.fr entry has null inner data plus a
// non-null array at position [5], that's an error chunk — we extract the
// first integer of the [5] array as ErrorCode (Google's internal code).
func decodeResponse(body []byte) ([]rpcResult, error) {
	if !bytes.HasPrefix(body, []byte(antiHijackPrefix)) {
		return nil, errors.New("response missing )]}' prefix — not a batchexecute body")
	}
	body = body[len(antiHijackPrefix):]
	body = bytes.TrimLeft(body, " \t\r\n")

	var results []rpcResult
	for len(body) > 0 {
		// Length: leading digits.
		i := 0
		for i < len(body) && body[i] >= '0' && body[i] <= '9' {
			i++
		}
		if i == 0 {
			break // no digits → end of meaningful content
		}
		chunkLen, err := strconv.Atoi(string(body[:i]))
		if err != nil {
			return nil, fmt.Errorf("parsing chunk length %q: %w", body[:i], err)
		}
		body = body[i:]
		body = bytes.TrimLeft(body, " \t\r\n")

		if len(body) < chunkLen {
			return nil, fmt.Errorf("chunk says %d bytes but only %d remain", chunkLen, len(body))
		}
		chunkJSON := body[:chunkLen]
		body = body[chunkLen:]
		body = bytes.TrimLeft(body, " \t\r\n")

		var chunkArr [][]interface{}
		if err := json.Unmarshal(chunkJSON, &chunkArr); err != nil {
			// Diagnostic chunks ("di", "e", "af.httprm") have shapes we don't
			// model; skip them silently.
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

			r := rpcResult{RpcID: rpcid}
			if entry[2] != nil {
				if inner, ok := entry[2].(string); ok {
					r.RawJSON = []byte(inner)
				}
			}
			// Position [5] holds error metadata when non-null. Format observed:
			//   ["wrb.fr", rpcid, null, null, null, [N], "generic"]
			// where N is Google's internal error code (1-N).
			if len(entry) >= 6 && entry[5] != nil {
				if errArr, ok := entry[5].([]interface{}); ok && len(errArr) > 0 {
					if code, ok := errArr[0].(float64); ok {
						r.ErrorCode = int(code)
					}
				}
			}
			results = append(results, r)
		}
	}
	return results, nil
}
