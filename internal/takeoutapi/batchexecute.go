// Package takeoutapi wraps Google Takeout's internal batchexecute endpoint.
// See docs/spikes/2026-05-02-batchexecute-api.md for protocol notes.
package takeoutapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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
//
// Format (after the )]}' prefix): a stream of JSON values where each "real"
// chunk (an array of arrays) is preceded by a number token (the chunk's
// declared byte length). The declared length is approximate / not always
// precise, so we IGNORE it and let json.Decoder tell us where each value
// actually ends.
//
// Optional whitespace between values is tolerated by json.Decoder
// automatically.
//
// Returns one rpcResult per "wrb.fr"-tagged entry across all chunks. When a
// wrb.fr entry has null inner data plus a non-null array at position [5],
// that's an error chunk — we extract the first integer of the [5] array as
// ErrorCode (Google's internal code).
func decodeResponse(body []byte) ([]rpcResult, error) {
	if !bytes.HasPrefix(body, []byte(antiHijackPrefix)) {
		return nil, errors.New("response missing )]}' prefix — not a batchexecute body")
	}
	body = body[len(antiHijackPrefix):]

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // so number tokens come back as json.Number, not float64

	var results []rpcResult
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			// Stop on first parse error. Don't fail hard — return what we have
			// in case some chunks parsed before the bad one.
			fmt.Fprintf(os.Stderr, "[takeoutapi]   decodeResponse: stopping at JSON err: %v\n", err)
			break
		}

		// Try to parse as a chunk array. Length-hint numbers between chunks
		// will fail this and get silently skipped.
		var chunkArr [][]interface{}
		if err := json.Unmarshal(raw, &chunkArr); err != nil {
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
			// where N is Google's internal error code.
			if len(entry) >= 6 && entry[5] != nil {
				if errArr, ok := entry[5].([]interface{}); ok && len(errArr) > 0 {
					switch v := errArr[0].(type) {
					case float64:
						r.ErrorCode = int(v)
					case json.Number:
						if n, err := v.Int64(); err == nil {
							r.ErrorCode = int(n)
						}
					}
				}
			}
			results = append(results, r)
		}
	}
	return results, nil
}
