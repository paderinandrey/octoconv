package presets

import "testing"

func TestValidateOptsJSON(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]any
		wantErr bool
	}{
		{
			name:    "nil map is valid",
			options: nil,
			wantErr: false,
		},
		{
			name:    "empty map is valid",
			options: map[string]any{},
			wantErr: false,
		},
		{
			name:    "valid DocOpts payload",
			options: map[string]any{"pdf_profile": "pdf/a-2b"},
			wantErr: false,
		},
		{
			name:    "valid HTMLOpts payload",
			options: map[string]any{"page_size": "a4", "margin_mm": 10},
			wantErr: false,
		},
		{
			name:    "key unknown to both schemas",
			options: map[string]any{"bogus": 1},
			wantErr: true,
		},
		{
			name:    "out-of-range HTML value, not DocOpts either",
			options: map[string]any{"margin_mm": 9999},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOptsJSON(tc.options)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
