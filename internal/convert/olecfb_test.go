package convert

import (
	"bytes"
	"testing"
)

func TestIsOLECFB(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "exact 8-byte CFB magic",
			data: []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1},
			want: true,
		},
		{
			name: "CFB magic with trailing content",
			data: append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, []byte("trailing sector bytes")...),
			want: true,
		},
		{
			name: "ZIP prefix is not CFB",
			data: []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00, 0x00, 0x00},
			want: false,
		},
		{
			name: "too-short input (7 of 8 magic bytes)",
			data: []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A},
			want: false,
		},
		{
			name: "empty input",
			data: []byte{},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsOLECFB(bytes.NewReader(tc.data))
			if got != tc.want {
				t.Fatalf("IsOLECFB(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
