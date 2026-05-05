package takeoutapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureTokens_RedirectToSigninErrorsCleanly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate Google's redirect to sign-in.
		http.Redirect(w, r, "https://accounts.google.com/v3/signin/identifier?continue=foo", http.StatusFound)
	}))
	defer srv.Close()

	c := newClientForTest(srv.URL, []*http.Cookie{{Name: "SID", Value: "stale"}})
	err := c.ensureTokens(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("error not ErrSessionExpired: %v", err)
	}
}
