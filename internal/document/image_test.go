package document

import "testing"

func TestIsImageName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid png",
			input: "image-0123456789abcdef.png",
			want:  true,
		},
		{
			name:  "valid jpg",
			input: "image-0123456789abcdef.jpg",
			want:  true,
		},
		{
			name:  "valid gif",
			input: "image-0123456789abcdef.gif",
			want:  true,
		},
		{
			name:  "valid all zeroes hash",
			input: "image-0000000000000000.png",
			want:  true,
		},
		{
			name:  "valid all letters hash",
			input: "image-aaaaaaaaaaaaaaaa.jpg",
			want:  true,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "path traversal prefix",
			input: "../image-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "path traversal suffix",
			input: "image-0123456789abcdef.png/../foo",
			want:  false,
		},
		{
			name:  "relative path directory",
			input: "images/image-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "unsupported extension jpeg",
			input: "image-0123456789abcdef.jpeg",
			want:  false,
		},
		{
			name:  "unsupported extension svg",
			input: "image-0123456789abcdef.svg",
			want:  false,
		},
		{
			name:  "unsupported extension txt",
			input: "image-0123456789abcdef.txt",
			want:  false,
		},
		{
			name:  "missing extension",
			input: "image-0123456789abcdef",
			want:  false,
		},
		{
			name:  "hash too short (15 hex chars)",
			input: "image-0123456789abcde.png",
			want:  false,
		},
		{
			name:  "hash too long (17 hex chars)",
			input: "image-0123456789abcdef0.png",
			want:  false,
		},
		{
			name:  "uppercase hex chars",
			input: "image-0123456789ABCDEF.png",
			want:  false,
		},
		{
			name:  "invalid non-hex char in hash",
			input: "image-0123456789abcdeg.png",
			want:  false,
		},
		{
			name:  "wrong prefix",
			input: "photo-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "missing prefix dash",
			input: "image0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "extra prefix character",
			input: "ximage-0123456789abcdef.png",
			want:  false,
		},
		{
			name:  "extra suffix character",
			input: "image-0123456789abcdef.pngx",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsImageName(tt.input)
			if got != tt.want {
				t.Errorf("IsImageName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
