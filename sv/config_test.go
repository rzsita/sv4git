package sv

import "testing"

func TestVersionPrefixFromPattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{
			name:    "v prefix",
			pattern: "v%d.%d.%d",
			want:    "v",
		},
		{
			name:    "no prefix",
			pattern: "%d.%d.%d",
			want:    "",
		},
		{
			name:    "longer prefix",
			pattern: "ver-%d.%d.%d",
			want:    "ver-",
		},
		{
			name:    "no %d at all",
			pattern: "latest",
			want:    "",
		},
		{
			name:    "empty pattern",
			pattern: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VersionPrefixFromPattern(tt.pattern)
			if got != tt.want {
				t.Errorf("VersionPrefixFromPattern(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}
