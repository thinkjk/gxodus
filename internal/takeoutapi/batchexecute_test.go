package takeoutapi

import (
	"net/url"
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
