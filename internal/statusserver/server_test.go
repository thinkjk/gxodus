package statusserver

import (
	"testing"
)

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.00 KB"},
		{1536, "1.50 KB"},
		{1048576, "1.00 MB"},
		{int64(2.5 * 1024 * 1024 * 1024), "2.50 GB"},
	}
	for _, tc := range cases {
		got := formatBytes(tc.in)
		if got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripTrailingNewlines(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"abc", "abc"},
		{"abc\n", "abc"},
		{"abc\r\n", "abc"},
		{"abc\n\n\n", "abc"},
		{"\n\n", ""},
	}
	for _, tc := range cases {
		got := string(stripTrailingNewlines([]byte(tc.in)))
		if got != tc.want {
			t.Errorf("stripTrailingNewlines(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNovncURL(t *testing.T) {
	cases := []struct {
		host string
		env  string
		want string
	}{
		{"192.168.1.10:6079", "", "http://192.168.1.10:6080/vnc.html"},
		{"unraid.local:6079", "", "http://unraid.local:6080/vnc.html"},
		{"localhost:6079", "8080", "http://localhost:8080/vnc.html"},
		{"", "", "http://localhost:6080/vnc.html"},
	}
	for _, tc := range cases {
		t.Setenv("GXODUS_NOVNC_PORT", tc.env)
		got := novncURL(tc.host)
		if got != tc.want {
			t.Errorf("novncURL(%q, env=%q) = %q, want %q", tc.host, tc.env, got, tc.want)
		}
	}
}
