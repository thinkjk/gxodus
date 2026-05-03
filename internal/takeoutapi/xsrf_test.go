package takeoutapi

import "testing"

func TestExtractTokens(t *testing.T) {
	// Realistic snippet of the page HTML — Google embeds tokens in WIZ_global_data.
	html := `<!DOCTYPE html><html><head>...</head><body>
<script>WIZ_global_data = {"SNlM0e":"ALYeEnkc1UxeQ3U_BuS-1yJoUbY8:1777768009152","cfb2h":"boq_identityfrontenduiserver_20260429.06_p0","other":"junk"};</script>
</body></html>`

	tokens, err := extractTokens(html)
	if err != nil {
		t.Fatalf("extractTokens: %v", err)
	}

	if tokens.XSRF != "ALYeEnkc1UxeQ3U_BuS-1yJoUbY8:1777768009152" {
		t.Errorf("XSRF = %q", tokens.XSRF)
	}
	if tokens.BuildLabel != "boq_identityfrontenduiserver_20260429.06_p0" {
		t.Errorf("BuildLabel = %q", tokens.BuildLabel)
	}
}

func TestExtractTokens_MissingXSRF(t *testing.T) {
	html := `<html><body>no tokens here</body></html>`
	if _, err := extractTokens(html); err == nil {
		t.Error("expected error when XSRF token absent")
	}
}

// TestExtractTokens_FirstCompleteBlock confirms that when multiple
// WIZ_global_data blocks exist, we pick the first one that has BOTH SNlM0e
// and cfb2h. Real takeout pages have a complete identity block followed by a
// stub second block (no cfb2h) — the first one is what batchexecute wants.
func TestExtractTokens_FirstCompleteBlock(t *testing.T) {
	html := `<html><body>
<script>WIZ_global_data = {"SNlM0e":"identity-xsrf:111","cfb2h":"boq_identityfrontenduiserver_20260429.06_p0","FdrFJe":"111"};</script>
<script>WIZ_global_data = {"some_other_field":"stub second block, no SNlM0e or cfb2h"};</script>
</body></html>`

	tokens, err := extractTokens(html)
	if err != nil {
		t.Fatalf("extractTokens: %v", err)
	}
	if tokens.XSRF != "identity-xsrf:111" {
		t.Errorf("XSRF = %q, want identity-xsrf:111", tokens.XSRF)
	}
	if tokens.BuildLabel != "boq_identityfrontenduiserver_20260429.06_p0" {
		t.Errorf("BuildLabel = %q", tokens.BuildLabel)
	}
	if tokens.SessionID != "111" {
		t.Errorf("SessionID = %q, want 111", tokens.SessionID)
	}
}
