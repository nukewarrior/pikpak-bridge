package domain

import "testing"

func TestInitialTaskName(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		sourceType string
		want       string
	}{
		{
			name:       "magnet dn",
			source:     "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&dn=Example%20Pack",
			sourceType: "magnet",
			want:       "Example Pack",
		},
		{
			name:       "magnet fallback",
			source:     "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567",
			sourceType: "magnet",
			want:       "Magnet 下载任务",
		},
		{
			name:       "http filename",
			source:     "https://example.invalid/downloads/movie%20name.mkv?token=abc",
			sourceType: "https",
			want:       "movie name.mkv",
		},
		{
			name:       "http host fallback",
			source:     "https://example.invalid/",
			sourceType: "https",
			want:       "example.invalid",
		},
		{
			name:       "ed2k filename",
			source:     "ed2k://|file|Example%20File.iso|123|ABCDEF|/",
			sourceType: "ed2k",
			want:       "Example File.iso",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InitialTaskName(tt.source, tt.sourceType); got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}
