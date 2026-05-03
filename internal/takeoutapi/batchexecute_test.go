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

func TestDecodeResponse_RealFormat_NoSeparators(t *testing.T) {
	// Captured shape from a real fhjYTc response: no newlines between segments.
	chunk1 := `[["wrb.fr","fhjYTc","[null,\"hello\"]",null,null,null,"generic"],["di",119]]`
	chunk2 := `[["e",4,null,null,200]]`
	body := []byte(")]}'" + strconv.Itoa(len(chunk1)) + chunk1 + strconv.Itoa(len(chunk2)) + chunk2)

	results, err := decodeResponse(body)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 wrb.fr result, got %d", len(results))
	}
	if results[0].RpcID != "fhjYTc" {
		t.Errorf("RpcID = %q", results[0].RpcID)
	}
	if string(results[0].RawJSON) != `[null,"hello"]` {
		t.Errorf("RawJSON = %s", results[0].RawJSON)
	}
	if results[0].ErrorCode != 0 {
		t.Errorf("ErrorCode = %d, want 0", results[0].ErrorCode)
	}
}

func TestDecodeResponse_TolerantOfNewlines(t *testing.T) {
	// Some Google responses (or our test synthesis) put \n between segments;
	// the decoder should tolerate either format.
	chunk1 := `[["wrb.fr","X","42",null,null,null,"generic"]]`
	body := []byte(")]}'\n" + strconv.Itoa(len(chunk1)) + "\n" + chunk1)

	results, err := decodeResponse(body)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if len(results) != 1 || string(results[0].RawJSON) != "42" {
		t.Errorf("results = %+v", results)
	}
}

func TestDecodeResponse_ErrorChunk(t *testing.T) {
	// Captured shape from a U5lrKc failure: position [2] is null, position [5]
	// is [3] meaning "error code 3". Decoder should surface the code.
	chunk := `[["wrb.fr","U5lrKc",null,null,null,[3],"generic"],["di",19]]`
	body := []byte(")]}'" + strconv.Itoa(len(chunk)) + chunk)

	results, err := decodeResponse(body)
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].RpcID != "U5lrKc" {
		t.Errorf("RpcID = %q", results[0].RpcID)
	}
	if len(results[0].RawJSON) != 0 {
		t.Errorf("RawJSON should be empty for error chunk, got %s", results[0].RawJSON)
	}
	if results[0].ErrorCode != 3 {
		t.Errorf("ErrorCode = %d, want 3", results[0].ErrorCode)
	}
}

func TestDecodeResponse_NoPrefixIsError(t *testing.T) {
	body := []byte(`{"oops": "json"}`)
	if _, err := decodeResponse(body); err == nil {
		t.Error("expected error for missing )]}' prefix")
	}
}
