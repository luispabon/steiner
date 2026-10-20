package lsp

import "testing"

func TestParsePositionInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input map[string]any
		want  positionInput
	}{
		{
			name:  "all present",
			input: map[string]any{"line": 3.0, "column": 4.0, "symbol": "Foo"},
			want:  positionInput{line: 3, lineOk: true, col: 4, colOk: true, symbol: "Foo", symbolOk: true},
		},
		{
			name:  "wrong types are absent",
			input: map[string]any{"line": "3", "column": true, "symbol": 7.0},
			want:  positionInput{},
		},
		{
			name:  "none present",
			input: map[string]any{},
			want:  positionInput{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePositionInput(tt.input); got != tt.want {
				t.Errorf("parsePositionInput() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolvePositionErrors(t *testing.T) {
	t.Parallel()
	m := &Manager{}
	tests := []struct {
		name    string
		pos     positionInput
		wantErr string
	}{
		{
			name:    "column without line",
			pos:     positionInput{col: 2, colOk: true},
			wantErr: "lsp_hover: column requires line",
		},
		{
			name:    "symbol with line below one",
			pos:     positionInput{symbol: "Foo", symbolOk: true, line: 0, lineOk: true},
			wantErr: "lsp_hover: invalid line 0",
		},
		{
			name:    "line without column or symbol",
			pos:     positionInput{line: 3, lineOk: true},
			wantErr: "lsp_hover: requires either column (with line) or symbol",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, echo, errMsg := resolvePosition(m, "lsp_hover", tt.pos, "main.go")
			if errMsg != tt.wantErr {
				t.Errorf("resolvePosition() error = %q, want %q", errMsg, tt.wantErr)
			}
			if echo != "" {
				t.Errorf("resolvePosition() echo = %q, want empty on error", echo)
			}
		})
	}
}
