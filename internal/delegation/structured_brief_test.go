package delegation

import "testing"

func TestParseStructuredBrief_NonStringListElements(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		input   []any
		wantErr string
	}{
		{
			name:    "constraints",
			field:   "constraints",
			input:   []any{"valid", 42},
			wantErr: "constraints[1] is not a string",
		},
		{
			name:    "success criteria",
			field:   "success_criteria",
			input:   []any{"valid", false},
			wantErr: "success_criteria[1] is not a string",
		},
		{
			name:    "checks",
			field:   "checks",
			input:   []any{"valid", nil},
			wantErr: "checks[1] is not a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validStructuredTask("task")
			input[tt.field] = tt.input

			for _, prefix := range []string{"explore", "vision"} {
				_, err := parseStructuredBrief(prefix, input)
				if err == nil {
					t.Fatalf("parseStructuredBrief(%q) returned nil error", prefix)
				}
				want := prefix + ": " + tt.wantErr
				if err.Error() != want {
					t.Errorf("error = %q, want %q", err.Error(), want)
				}
			}
		})
	}
}
