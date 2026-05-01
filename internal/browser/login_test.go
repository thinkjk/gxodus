package browser

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindLoggedInTab(t *testing.T) {
	tests := []struct {
		name string
		tabs []devtoolsTab
		want bool
	}{
		{
			name: "myaccount tab logged in",
			tabs: []devtoolsTab{{Type: "page", URL: "https://myaccount.google.com/"}},
			want: true,
		},
		{
			name: "drive tab logged in",
			tabs: []devtoolsTab{{Type: "page", URL: "https://drive.google.com/drive/my-drive"}},
			want: true,
		},
		{
			name: "still on login page",
			tabs: []devtoolsTab{{Type: "page", URL: "https://accounts.google.com/signin"}},
			want: false,
		},
		{
			name: "non-page targets ignored",
			tabs: []devtoolsTab{{Type: "service_worker", URL: "https://myaccount.google.com/sw.js"}},
			want: false,
		},
		{
			name: "multiple tabs, one logged in",
			tabs: []devtoolsTab{
				{Type: "page", URL: "https://accounts.google.com/signin"},
				{Type: "page", URL: "https://mail.google.com/mail/u/0/#inbox"},
			},
			want: true,
		},
		{
			name: "no tabs",
			tabs: nil,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findLoggedInTab(tt.tabs); got != tt.want {
				t.Errorf("findLoggedInTab() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBrowserWebSocketURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl": "ws://localhost:9222/devtools/browser/abc-123"}`))
	}))
	defer srv.Close()

	got, err := getBrowserWebSocketURL(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "ws://localhost:9222/devtools/browser/abc-123"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetBrowserWebSocketURLEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	if _, err := getBrowserWebSocketURL(srv.URL); err == nil {
		t.Error("expected error when webSocketDebuggerUrl missing")
	}
}
