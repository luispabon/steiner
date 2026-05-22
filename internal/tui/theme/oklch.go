package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// OklchToHex converts OKLCH(L, C, H) to a hex color string.
// L: lightness [0, 1]
// C: chroma [0, 1]
// H: hue [0, 360]
// Returns: hex string with "#" prefix (e.g. "#1C1B21")
func OklchToHex(l, c, h float64) string {
	// OKLCH → OKLab
	hRad := h * math.Pi / 180.0
	a := c * math.Cos(hRad)
	b := c * math.Sin(hRad)

	// OKLab → linear sRGB
	lPrime := l + 0.3963377774*a + 0.2158037573*b
	mPrime := l - 0.1055613458*a - 0.0638541728*b
	sPrime := l - 0.0894841775*a - 1.2914855480*b

	lCube := lPrime * lPrime * lPrime
	mCube := mPrime * mPrime * mPrime
	sCube := sPrime * sPrime * sPrime

	rLin := 4.0767416621*lCube - 3.3077115913*mCube + 0.2309699292*sCube
	gLin := -1.2684380046*lCube + 2.6097574011*mCube - 0.3413193965*sCube
	bLin := -0.0041960863*lCube - 0.7034186147*mCube + 1.7076147010*sCube

	// Linear sRGB → gamma sRGB
	rGamma := linearToSRGB(rLin)
	gGamma := linearToSRGB(gLin)
	bGamma := linearToSRGB(bLin)

	// Clamp to [0, 1]
	rGamma = clamp(rGamma, 0, 1)
	gGamma = clamp(gGamma, 0, 1)
	bGamma = clamp(bGamma, 0, 1)

	// Convert to uint8 and hex
	rByte := uint8(math.Round(rGamma * 255))
	gByte := uint8(math.Round(gGamma * 255))
	bByte := uint8(math.Round(bGamma * 255))

	return fmt.Sprintf("#%02X%02X%02X", rByte, gByte, bByte)
}

// linearToSRGB applies gamma curve correction from linear to sRGB.
func linearToSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1.0/2.4) - 0.055
}

// clamp restricts a value to [min, max].
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// blendHex blends two hex colors together.
// colorHex: foreground color
// bgHex: background color
// alpha: blend amount for foreground [0, 1]
// Returns: blended hex color
func blendHex(colorHex, bgHex string, alpha float64) string {
	rFg, gFg, bFg := hexToRGB(colorHex)
	rBg, gBg, bBg := hexToRGB(bgHex)

	rBlend := uint8(math.Round(float64(rFg)*alpha + float64(rBg)*(1-alpha)))
	gBlend := uint8(math.Round(float64(gFg)*alpha + float64(gBg)*(1-alpha)))
	bBlend := uint8(math.Round(float64(bFg)*alpha + float64(bBg)*(1-alpha)))

	return fmt.Sprintf("#%02X%02X%02X", rBlend, gBlend, bBlend)
}

// hexToRGB parses a hex color string and returns RGB components.
func hexToRGB(hex string) (uint8, uint8, uint8) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 64)
	g, _ := strconv.ParseInt(hex[2:4], 16, 64)
	b, _ := strconv.ParseInt(hex[4:6], 16, 64)
	return uint8(r), uint8(g), uint8(b)
}

// Design token colors pre-computed from OKLCH values
const (
	// Neutral palette
	Bg         = "#000000" // OklchToHex(0.16,  0.005, 280)
	BgElev     = "#0a0a0a" // OklchToHex(0.185, 0.005, 280)
	BgElev2    = "#333238" // OklchToHex(0.21,  0.006, 280)
	BgInput    = "#2B2A30" // OklchToHex(0.20,  0.005, 280)
	Border     = "#46444D" // OklchToHex(0.27,  0.006, 280)
	BorderSoft = "#3A393F" // OklchToHex(0.23,  0.005, 280)
	Fg         = "#d8d8d8" // OklchToHex(0.93,  0.005, 280)
	FgDim      = "#9a9a9a" // OklchToHex(0.62,  0.008, 280)
	FgFaint    = "#6a6a6a" // OklchToHex(0.45,  0.008, 280)
	FgMute     = "#4a4a4a" // OklchToHex(0.35,  0.008, 280)
	FgLabel    = "#b4b4b4" // OklchToHex(0.75,  0.005, 280)

	// Semantic colors
	AccentAmber   = "#E8814B" // OklchToHex(0.74,  0.16,  35)
	User          = "#6DB3E8" // OklchToHex(0.78,  0.13,  230)
	Thinking      = "#e6c250" // OklchToHex(0.70,  0.09,  75)
	Tool          = "#5BA8D9" // OklchToHex(0.72,  0.07,  195)
	Added         = "#87d75f" // OklchToHex(0.74,  0.14,  150)
	Removed       = "#e85a3a" // OklchToHex(0.68,  0.17,  25)
	Warn          = "#e6a93a" // OklchToHex(0.78,  0.14,  80)
	DiffAddedBg   = "#1F2A24" // dim green tint for + diff rows
	DiffRemovedBg = "#2A2122" // dim red tint for - diff rows

	ToolBlue       = "#5fafff" // search, glob
	ToolCyan       = "#5fd7d7" // read
	ToolGrn        = "#87d75f" // write, bash ok
	ToolMag        = "#d75fd7" // grep
	DelegateViolet = "#9D8DF1" // research delegate
	Black          = "#000000" // tag chip foreground
	SyntaxBlue     = "#5f8fff" // keyword blue (file preview)
)

// Soft fill colors (blended with Bg)
var (
	AccentSoft  = blendHex(AccentAmber, Bg, 0.09) // 9% accent + 91% bg
	UserSoft    = blendHex(User, Bg, 0.12)        // 12% user + 88% bg
	ToolSoft    = blendHex(Tool, Bg, 0.08)        // 8% tool + 92% bg
	AddedSoft   = blendHex(Added, Bg, 0.09)       // 9% added + 91% bg
	RemovedSoft = blendHex(Removed, Bg, 0.09)     // 9% removed + 91% bg
	WarnSoft    = blendHex(Warn, Bg, 0.07)        // 7% warn + 93% bg
	AccentLine  = blendHex(AccentAmber, Bg, 0.35) // 35% accent + 65% bg

	// Muted tool border colors (blended with Bg)
	ToolAmberLine = blendHex(AccentAmber, Bg, 0.30) // 30% bash + 70% bg
	ToolCyanLine  = blendHex(ToolCyan, Bg, 0.30)    // 30% read + 70% bg
	ToolGrnLine   = blendHex(ToolGrn, Bg, 0.30)     // 30% write + 70% bg
	ToolMagLine   = blendHex(ToolMag, Bg, 0.30)     // 30% grep + 70% bg
	ToolBlueLine  = blendHex(ToolBlue, Bg, 0.30)    // 30% search/glob + 70% bg
	WarnLine      = blendHex(Warn, Bg, 0.30)        // 30% todo + 70% bg

	// Muted delegation border colors (blended with Bg, per agent type)
	DelegateVioletLine   = blendHex(DelegateViolet, Bg, 0.30) // research
	DelegateThinkingLine = blendHex(Thinking, Bg, 0.30)       // plan
)

// AccentPresets maps accent preset names to their hex values
var AccentPresets = map[string]string{
	"amber":   "#E8814B",
	"rose":    "#E36F8E",
	"magenta": "#C977D3",
	"violet":  "#9D8DF1",
	"cyan":    "#5EC9D6",
	"mint":    "#6FCFA3",
	"lime":    "#B6D45F",
}
