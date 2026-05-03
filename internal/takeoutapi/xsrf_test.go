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

// TestExtractTokens_PrefersTakeoutBlock guards against a regression where the
// takeout page embeds multiple WIZ_global_data blocks (one for an identity
// widget, one for the actual TakeoutUi product). batchexecute rejects
// (error code 3) the identity-block tokens, so we must pick the takeoutui one.
func TestExtractTokens_PrefersTakeoutBlock(t *testing.T) {
	html := `<html><body>
<script>WIZ_global_data = {"SNlM0e":"identity-xsrf:111","cfb2h":"boq_identityfrontenduiserver_20260429.06_p0","FdrFJe":"111"};</script>
<script>WIZ_global_data = {"SNlM0e":"takeout-xsrf:222","cfb2h":"boq_takeoutuiserver_20260429.06_p0","FdrFJe":"222"};</script>
</body></html>`

	tokens, err := extractTokens(html)
	if err != nil {
		t.Fatalf("extractTokens: %v", err)
	}
	if tokens.XSRF != "takeout-xsrf:222" {
		t.Errorf("XSRF = %q, want takeout-xsrf:222", tokens.XSRF)
	}
	if tokens.BuildLabel != "boq_takeoutuiserver_20260429.06_p0" {
		t.Errorf("BuildLabel = %q, want takeoutui build", tokens.BuildLabel)
	}
	if tokens.SessionID != "222" {
		t.Errorf("SessionID = %q, want 222", tokens.SessionID)
	}
}
