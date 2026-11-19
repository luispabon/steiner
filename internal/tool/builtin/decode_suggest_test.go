package builtin

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeInputUnknownFieldSuggestions(t *testing.T) {
	t.Run("containment suggests longer field", func(t *testing.T) {
		_, err := decodeInput[GrepInput](map[string]any{
			"pattern": "x",
			"limit":   5,
		})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "limit", "head_limit")
	})

	t.Run("transposition suggests pattern", func(t *testing.T) {
		_, err := decodeInput[GrepInput](map[string]any{"patern": "x"})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "patern", "pattern")
	})

	t.Run("reordered transposition suggests pattern", func(t *testing.T) {
		_, err := decodeInput[GrepInput](map[string]any{"pattren": "x"})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "pattren", "pattern")
	})

	t.Run("normalized equality ignores casing and separators", func(t *testing.T) {
		tests := []struct {
			key  string
			want string
		}{
			{key: "Pattern", want: "pattern"},
			{key: "headLimit", want: "head_limit"},
			{key: "Head-Limit", want: "head_limit"},
		}
		for _, tc := range tests {
			t.Run(tc.key, func(t *testing.T) {
				_, err := decodeInput[GrepInput](map[string]any{tc.key: "x"})
				msg := requireUnknownFieldError(t, err)
				requireSuggestion(t, msg, tc.key, tc.want)
			})
		}
	})

	t.Run("present target is not suggested", func(t *testing.T) {
		_, err := decodeInput[GrepInput](map[string]any{
			"pattern": "x",
			"patern":  "y",
		})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for an already present target", msg)
		}
	})

	t.Run("reverse containment suggests shorter field", func(t *testing.T) {
		_, err := decodeInput[reverseContainmentInput](map[string]any{"path_glob": "x"})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "path_glob", "path")
	})

	t.Run("acronym containment uses split tokens", func(t *testing.T) {
		_, err := decodeInput[acronymContainmentInput](map[string]any{"fileURLPath": "x"})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "fileURLPath", "url")
	})
}

func TestDecodeInputUnknownFieldSuggestionAmbiguity(t *testing.T) {
	t.Run("normalized equality tie yields no suggestion", func(t *testing.T) {
		_, err := decodeInput[suggestionAmbiguousInput](map[string]any{"FOO-BAR": "x"})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for an ambiguous field", msg)
		}
	})

	t.Run("equal edit distance tie yields no suggestion", func(t *testing.T) {
		_, err := decodeInput[suggestionOSATieInput](map[string]any{"abcf": "x"})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for an edit distance tie", msg)
		}
	})
}

func TestDecodeInputUnknownFieldPresentBestTier(t *testing.T) {
	_, err := decodeInput[presentTierInput](map[string]any{
		"path": "x",
		"Path": "y",
	})
	msg := requireUnknownFieldError(t, err)
	if strings.Contains(msg, `did you mean "path_glob"`) {
		t.Fatalf("error = %q, want no weaker suggestion for an already present best match", msg)
	}
	if strings.Contains(msg, "did you mean") {
		t.Fatalf("error = %q, want the present best match to suppress suggestions", msg)
	}
}

func TestSplitFieldNameAcronymBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "trailing acronym", input: "fileURLPath", want: []string{"file", "url", "path"}},
		{name: "leading acronym", input: "XMLHttpRequest", want: []string{"xml", "http", "request"}},
		{name: "acronym before digit", input: "HTTP2Server", want: []string{"http2", "server"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitFieldName(tc.input); !slices.Equal(got, tc.want) {
				t.Fatalf("splitFieldName(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestDecodeInputUnknownFieldSuggestionCollision(t *testing.T) {
	_, err := decodeInput[GrepInput](map[string]any{
		"patern":  "x",
		"pattren": "y",
	})
	msg := requireUnknownFieldError(t, err)
	for _, key := range []string{"patern", "pattren"} {
		if !strings.Contains(msg, "unknown field "+strconv.Quote(key)) {
			t.Fatalf("error = %q, want unknown field %q", msg, key)
		}
	}
	if strings.Contains(msg, "did you mean") {
		t.Fatalf("error = %q, want colliding suggestions suppressed", msg)
	}
}

func TestDecodeInputUnknownFieldSuggestionLengths(t *testing.T) {
	t.Run("short key never matches", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "x",
			"lim":  "y",
		})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for a three-rune key", msg)
		}
	})

	t.Run("distance one matches four to seven runes", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "x",
			"limt": "y",
		})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "limt", "limit")
	})

	t.Run("distance two is rejected below eight runes", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "x",
			"limy": "y",
		})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for distance two at four runes", msg)
		}
	})

	t.Run("distance two matches eight runes or more", func(t *testing.T) {
		_, err := decodeInput[BashInput](map[string]any{
			"command":          "x",
			"max_output_bytzz": 5,
		})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, "max_output_bytzz", "max_output_bytes")
	})

	t.Run("long key is not matched and is bounded", func(t *testing.T) {
		longKey := strings.Repeat("max_output_byte", 5)
		_, err := decodeInput[BashInput](map[string]any{
			"command": "x",
			longKey:   5,
		})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for a key over %d runes", msg, maxSuggestedKeyRunes)
		}
		if !strings.Contains(msg, "...") {
			t.Fatalf("error = %q, want a truncated key marker", msg)
		}
		if len(msg) > 400 {
			t.Fatalf("error length = %d, want <= 400", len(msg))
		}
	})
	t.Run("sixty-four rune key matches", func(t *testing.T) {
		if len(lengthBoundaryFieldName) != maxSuggestedKeyRunes {
			t.Fatalf("test field name length = %d, want %d", len(lengthBoundaryFieldName), maxSuggestedKeyRunes)
		}
		key := strings.ToUpper(lengthBoundaryFieldName)
		_, err := decodeInput[lengthBoundaryInput](map[string]any{key: "x"})
		msg := requireUnknownFieldError(t, err)
		requireSuggestion(t, msg, key, lengthBoundaryFieldName)
		if strings.Contains(msg, "...") {
			t.Fatalf("error = %q, want a %d-rune key shown in full", msg, maxSuggestedKeyRunes)
		}
	})

	t.Run("sixty-five rune key is skipped", func(t *testing.T) {
		key := strings.Repeat("a", maxSuggestedKeyRunes+1)
		_, err := decodeInput[lengthBoundaryInput](map[string]any{key: "x"})
		msg := requireUnknownFieldError(t, err)
		if strings.Contains(msg, "did you mean") {
			t.Fatalf("error = %q, want no suggestion for a key over %d runes", msg, maxSuggestedKeyRunes)
		}
		if !strings.Contains(msg, "...") {
			t.Fatalf("error = %q, want the key over %d runes truncated", msg, maxSuggestedKeyRunes)
		}
	})
}

func TestDecodeInputUnknownFieldErrorBounds(t *testing.T) {
	t.Run("reports three sorted fields and the omitted count", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "x",
			"eee":  "1",
			"ccc":  "1",
			"aaa":  "1",
			"ddd":  "1",
			"bbb":  "1",
		})
		msg := requireUnknownFieldError(t, err)
		want := `unknown field "aaa"; unknown field "bbb"; unknown field "ccc"; 2 more unknown fields; valid fields:`
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want to contain %q", msg, want)
		}
		if strings.Contains(msg, "ddd") || strings.Contains(msg, "eee") {
			t.Fatalf("error = %q, want only the first three unknown fields", msg)
		}
	})

	t.Run("keeps real GrepInput valid field list complete", func(t *testing.T) {
		_, err := decodeInput[GrepInput](map[string]any{
			"pattern": "x",
			"zzzzz":   "y",
		})
		msg := requireUnknownFieldError(t, err)
		wantFields := []string{
			"after_context",
			"before_context",
			"case_insensitive",
			"context",
			"glob",
			"head_limit",
			"line_numbers",
			"multiline",
			"offset",
			"output_mode",
			"path",
			"pattern",
			"type",
		}
		want := "valid fields: " + strings.Join(wantFields, ", ")
		if !strings.HasSuffix(msg, want) {
			t.Fatalf("error = %q, want complete valid fields %q", msg, want)
		}
		if strings.Contains(msg, "(+") {
			t.Fatalf("error = %q, want no omitted real valid fields", msg)
		}
	})

	t.Run("bounds the sorted valid field list", func(t *testing.T) {
		_, err := decodeInput[suggestionManyFieldsInput](map[string]any{"zzzzz": "x"})
		msg := requireUnknownFieldError(t, err)
		if !strings.Contains(msg, "field_01") || !strings.Contains(msg, "field_24") {
			t.Fatalf("error = %q, want the first twenty-four valid fields", msg)
		}
		if !strings.Contains(msg, "(+4 more)") {
			t.Fatalf("error = %q, want an omitted valid field count", msg)
		}
		if strings.Contains(msg, "field_25") {
			t.Fatalf("error = %q, want the valid field list bounded", msg)
		}
	})

	t.Run("escapes the unknown field name", func(t *testing.T) {
		key := "bad\"key\n"
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "x",
			key:    "y",
		})
		msg := requireUnknownFieldError(t, err)
		if !strings.Contains(msg, strconv.Quote(key)) {
			t.Fatalf("error = %q, want escaped key %q", msg, strconv.Quote(key))
		}
		if strings.Contains(msg, "\n") {
			t.Fatalf("error = %q, want no raw newline", msg)
		}
	})
}

func TestDecodeInputUnknownFieldKeepsNestedPath(t *testing.T) {
	_, err := decodeInput[MutateInput](map[string]any{
		"operations": []any{
			map[string]any{
				"type":      "replace",
				"path":      "x",
				"old_strng": "a",
			},
		},
	})
	msg := requireUnknownFieldError(t, err)
	for _, want := range []string{`field "operations"`, "element 0", `unknown field "old_strng"`, `did you mean "old_string"`} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want to contain %q", msg, want)
		}
	}
}

func TestDecodeInputUnknownFieldDoesNotMutateInput(t *testing.T) {
	raw := map[string]any{
		"pattern": "x",
		"patern":  "y",
		"nested":  map[string]any{"items": []any{map[string]any{"n": float64(1)}}},
	}
	want := map[string]any{
		"pattern": "x",
		"patern":  "y",
		"nested":  map[string]any{"items": []any{map[string]any{"n": float64(1)}}},
	}

	if _, err := decodeInput[GrepInput](raw); err == nil {
		t.Fatal("decodeInput error = nil, want unknown field error")
	}
	if !reflect.DeepEqual(raw, want) {
		t.Fatalf("raw input = %#v, want unchanged %#v", raw, want)
	}
}

func requireUnknownFieldError(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("decodeInput error = nil, want unknown field error")
	}
	msg := err.Error()
	for _, want := range []string{"unknown field", "valid fields:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want to contain %q", msg, want)
		}
	}
	return msg
}

func requireSuggestion(t *testing.T, msg, key, target string) {
	t.Helper()
	want := "unknown field " + strconv.Quote(key) + " (did you mean " + strconv.Quote(target) + "?)"
	if !strings.Contains(msg, want) {
		t.Fatalf("error = %q, want to contain %q", msg, want)
	}
}

type suggestionAmbiguousInput struct {
	Snake string `json:"foo_bar"`
	Camel string `json:"fooBar"`
}

type suggestionOSATieInput struct {
	First  string `json:"abcd"`
	Second string `json:"abce"`
}

type suggestionManyFieldsInput struct {
	Field01 string `json:"field_01"`
	Field02 string `json:"field_02"`
	Field03 string `json:"field_03"`
	Field04 string `json:"field_04"`
	Field05 string `json:"field_05"`
	Field06 string `json:"field_06"`
	Field07 string `json:"field_07"`
	Field08 string `json:"field_08"`
	Field09 string `json:"field_09"`
	Field10 string `json:"field_10"`
	Field11 string `json:"field_11"`
	Field12 string `json:"field_12"`
	Field13 string `json:"field_13"`
	Field14 string `json:"field_14"`
	Field15 string `json:"field_15"`
	Field16 string `json:"field_16"`
	Field17 string `json:"field_17"`
	Field18 string `json:"field_18"`
	Field19 string `json:"field_19"`
	Field20 string `json:"field_20"`
	Field21 string `json:"field_21"`
	Field22 string `json:"field_22"`
	Field23 string `json:"field_23"`
	Field24 string `json:"field_24"`
	Field25 string `json:"field_25"`
	Field26 string `json:"field_26"`
	Field27 string `json:"field_27"`
	Field28 string `json:"field_28"`
}

type presentTierInput struct {
	Path     string `json:"path"`
	PathGlob string `json:"path_glob"`
}

type reverseContainmentInput struct {
	Path string `json:"path"`
}

type acronymContainmentInput struct {
	URL string `json:"url"`
}

const lengthBoundaryFieldName = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type lengthBoundaryInput struct {
	Value string `json:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`
}
