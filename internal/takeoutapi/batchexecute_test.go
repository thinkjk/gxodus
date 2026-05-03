package takeoutapi

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestEncodeRequest(t *testing.T) {
	body, err := encodeRequest("U5lrKc", `[["bond"],["drive"]]`, "generic", "AT_TOKEN:1777768009152")
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}

	parsed, err := url.ParseQuery(body)
	if err != nil {
		t.Fatalf("parsing body: %v", err)
	}

	gotFreq := parsed.Get("f.req")
	wantFreq := `[[["U5lrKc","[[\"bond\"],[\"drive\"]]",null,"generic"]]]`
	if gotFreq != wantFreq {
		t.Errorf("f.req = %q, want %q", gotFreq, wantFreq)
	}

	if got := parsed.Get("at"); got != "AT_TOKEN:1777768009152" {
		t.Errorf("at = %q, want %q", got, "AT_TOKEN:1777768009152")
	}

	// Also sanity-check the body has both keys, not e.g. duplicated.
	hasFreq := strings.Contains(body, "f.req=")
	hasAt := strings.Contains(body, "at=")
	if !hasFreq || !hasAt {
		t.Errorf("body missing expected keys: %s", body)
	}
}

func TestDecodeResponse(t *testing.T) {
	// Build a captured-shape response. Compute chunk lengths dynamically so
	// they always match the actual JSON byte length.
	chunk1 := `[["wrb.fr","fhjYTc","[null,\"hello\"]",null,null,null,"generic"]]`
	chunk2 := `[["di",123],["af.httprm",122,"-8797266961199462245",9]]`
	chunk3 := `[["e",4,null,null,30387]]`

	body := []byte(")]}'\n" +
		strconv.Itoa(len(chunk1)) + "\n" + chunk1 + "\n" +
		strconv.Itoa(len(chunk2)) + "\n" + chunk2 + "\n" +
		strconv.Itoa(len(chunk3)) + "\n" + chunk3)

	results, err := decodeResponse(body)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].RpcID != "fhjYTc" {
		t.Errorf("RpcID = %q, want %q", results[0].RpcID, "fhjYTc")
	}

	if string(results[0].RawJSON) != `[null,"hello"]` {
		t.Errorf("RawJSON = %s, want %s", results[0].RawJSON, `[null,"hello"]`)
	}
}

func TestDecodeResponse_StripsAntiHijackPrefix(t *testing.T) {
	body := []byte(")]}'\n" + "10\n" + `["wrb.fr"]`)
	if _, err := decodeResponse(body); err != nil {
		t.Errorf("expected no error after stripping prefix: %v", err)
	}
}

func TestDecodeResponse_NoPrefixIsError(t *testing.T) {
	body := []byte(`{"oops": "json"}`)
	if _, err := decodeResponse(body); err == nil {
		t.Error("expected error for missing )]}' prefix")
	}
}
