package theme

import (
	"testing"
)

func TestOklchToHex(t *testing.T) {
	tests := []struct {
		name    string
		l, c, h float64
		want    string
	}{
		{name: "black (all zero)", l: 0, c: 0, h: 0, want: "#000000"},
		{name: "white (achromatic)", l: 1, c: 0, h: 0, want: "#FFFFFF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OklchToHex(tt.l, tt.c, tt.h); got != tt.want {
				t.Errorf("OklchToHex(%v, %v, %v) = %v, want %v", tt.l, tt.c, tt.h, got, tt.want)
			}
		})
	}
}

func TestBlendHex(t *testing.T) {
	tests := []struct {
		name   string
		fg, bg string
		alpha  float64
		want   string
	}{
		{name: "alpha 1 picks fg", fg: "#FF0000", bg: "#00FF00", alpha: 1, want: "#FF0000"},
		{name: "alpha 0 picks bg", fg: "#FF0000", bg: "#00FF00", alpha: 0, want: "#00FF00"},
		{name: "50-50 red-blue", fg: "#FF0000", bg: "#0000FF", alpha: 0.5, want: "#800080"},
		{name: "75-25 white-black", fg: "#FFFFFF", bg: "#000000", alpha: 0.75, want: "#BFBFBF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blendHex(tt.fg, tt.bg, tt.alpha); got != tt.want {
				t.Errorf("blendHex(%q, %q, %v) = %v, want %v", tt.fg, tt.bg, tt.alpha, got, tt.want)
			}
		})
	}
}

func TestToolBorderLineColors(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "bash", got: ToolAmberLine, want: blendHex(AccentAmber, Bg, 0.30)},
		{name: "read", got: ToolCyanLine, want: blendHex(ToolCyan, Bg, 0.30)},
		{name: "write", got: ToolGrnLine, want: blendHex(ToolGrn, Bg, 0.30)},
		{name: "grep", got: ToolMagLine, want: blendHex(ToolMag, Bg, 0.30)},
		{name: "search", got: ToolBlueLine, want: blendHex(ToolBlue, Bg, 0.30)},
		{name: "todo", got: WarnLine, want: blendHex(Warn, Bg, 0.30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestAccentPresets(t *testing.T) {
	if len(AccentPresets) != 13 {
		t.Errorf("AccentPresets has %d entries, want 13", len(AccentPresets))
	}

	// Valid hex regex pattern: #[0-9A-Fa-f]{6}
	for name, hex := range AccentPresets {
		if len(hex) != 7 || hex[0] != '#' {
			t.Errorf("AccentPresets[%q] = %q is not a valid hex color", name, hex)
			continue
		}
		for i := 1; i < 7; i++ {
			c := hex[i]
			if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
				t.Errorf("AccentPresets[%q] = %q contains invalid hex character %c", name, hex, c)
			}
		}
	}
}

func TestHexToRGB(t *testing.T) {
	tests := []struct {
		name  string
		hex   string
		wantR uint8
		wantG uint8
		wantB uint8
	}{
		{name: "red", hex: "#FF0000", wantR: 255, wantG: 0, wantB: 0},
		{name: "green", hex: "#00FF00", wantR: 0, wantG: 255, wantB: 0},
		{name: "blue", hex: "#0000FF", wantR: 0, wantG: 0, wantB: 255},
		{name: "white", hex: "#FFFFFF", wantR: 255, wantG: 255, wantB: 255},
		{name: "without hash", hex: "FFAABB", wantR: 255, wantG: 170, wantB: 187},
		{name: "empty string", hex: "", wantR: 0, wantG: 0, wantB: 0},
		{name: "short hex", hex: "#FFF", wantR: 0, wantG: 0, wantB: 0},
		{name: "5-digit hex", hex: "#12345", wantR: 0, wantG: 0, wantB: 0},
		{name: "invalid hex chars", hex: "#GGGGGG", wantR: 0, wantG: 0, wantB: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, g, b := hexToRGB(tt.hex)
			if r != tt.wantR || g != tt.wantG || b != tt.wantB {
				t.Errorf("hexToRGB(%q) = (%d, %d, %d), want (%d, %d, %d)",
					tt.hex, r, g, b, tt.wantR, tt.wantG, tt.wantB)
			}
		})
	}
}
