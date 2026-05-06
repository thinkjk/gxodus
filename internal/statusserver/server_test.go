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
