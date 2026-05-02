package browser

import "testing"

func TestFileTypeDisplayText(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ".zip", false},
		{"zip", ".zip", false},
		{"tgz", ".tgz", false},
		{"rar", "", true},
	}
	for _, tt := range tests {
		got, err := fileTypeDisplayText(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("fileTypeDisplayText(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("fileTypeDisplayText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFileSizeDisplayText(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "2 GB", false},
		{"1GB", "1 GB", false},
		{"2GB", "2 GB", false},
		{"4GB", "4 GB", false},
		{"10GB", "10 GB", false},
		{"50GB", "50 GB", false},
		{"3GB", "", true},
	}
	for _, tt := range tests {
		got, err := fileSizeDisplayText(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("fileSizeDisplayText(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("fileSizeDisplayText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFrequencyRadioValue(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "1", false},
		{"once", "1", false},
		{"every_2_months", "2", false},
		{"weekly", "", true},
	}
	for _, tt := range tests {
		got, err := frequencyRadioValue(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("frequencyRadioValue(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("frequencyRadioValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
