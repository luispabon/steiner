package agent

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestSegmentedCompactionPolicyDefaults(t *testing.T) {
	want := SegmentedCompactionPolicy{
		Enabled:                  false,
		MaxSegments:              8,
		MaxChainPercent:          12.5,
		MaxPostCompactionPercent: 50,
	}
	if got := DefaultSegmentedCompactionPolicy(); got != want {
		t.Fatalf("DefaultSegmentedCompactionPolicy() = %+v, want %+v", got, want)
	}

	var zero SegmentedCompactionPolicy
	if zero.Enabled {
		t.Fatal("zero SegmentedCompactionPolicy is enabled, want disabled")
	}

	got, err := ParseSegmentedCompactionPolicy(nil)
	if err != nil {
		t.Fatalf("ParseSegmentedCompactionPolicy(nil) error = %v", err)
	}
	if got != want {
		t.Fatalf("ParseSegmentedCompactionPolicy(nil) = %+v, want %+v", got, want)
	}
}

func TestSegmentedCompactionPolicyParsing(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    SegmentedCompactionPolicy
		wantErr string
	}{
		{
			name: "unset keeps defaults",
			want: DefaultSegmentedCompactionPolicy(),
		},
		{
			name: "blank values keep defaults",
			env: map[string]string{
				SegmentedCompactionEnv:    "   ",
				SegmentMaxCountEnv:        "\t",
				SegmentMaxChainPercentEnv: " ",
				SegmentMaxPostPercentEnv:  "",
			},
			want: DefaultSegmentedCompactionPolicy(),
		},
		{
			name: "enabled true",
			env:  map[string]string{SegmentedCompactionEnv: "true"},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.Enabled = true
				return p
			}(),
		},
		{
			name: "enabled 1 with surrounding space",
			env:  map[string]string{SegmentedCompactionEnv: " 1 "},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.Enabled = true
				return p
			}(),
		},
		{
			name: "enabled 0",
			env:  map[string]string{SegmentedCompactionEnv: "0"},
			want: DefaultSegmentedCompactionPolicy(),
		},
		{
			name: "all thresholds overridden",
			env: map[string]string{
				SegmentedCompactionEnv:    "1",
				SegmentMaxCountEnv:        "16",
				SegmentMaxChainPercentEnv: "20",
				SegmentMaxPostPercentEnv:  "75.5",
			},
			want: SegmentedCompactionPolicy{
				Enabled:                  true,
				MaxSegments:              16,
				MaxChainPercent:          20,
				MaxPostCompactionPercent: 75.5,
			},
		},
		{
			name: "lower count bound",
			env:  map[string]string{SegmentMaxCountEnv: "1"},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.MaxSegments = 1
				return p
			}(),
		},
		{
			name: "upper count bound",
			env:  map[string]string{SegmentMaxCountEnv: "64"},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.MaxSegments = 64
				return p
			}(),
		},
		{
			name: "upper percent bound",
			env:  map[string]string{SegmentMaxChainPercentEnv: "100", SegmentMaxPostPercentEnv: "100"},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.MaxChainPercent = 100
				p.MaxPostCompactionPercent = 100
				return p
			}(),
		},
		{
			name: "tiny positive percent",
			env:  map[string]string{SegmentMaxChainPercentEnv: "0.01", SegmentMaxPostPercentEnv: "1e-09"},
			want: func() SegmentedCompactionPolicy {
				p := DefaultSegmentedCompactionPolicy()
				p.MaxChainPercent = 0.01
				p.MaxPostCompactionPercent = 1e-09
				return p
			}(),
		},
		{
			name:    "enabled not a bool",
			env:     map[string]string{SegmentedCompactionEnv: "yes"},
			wantErr: "parse " + SegmentedCompactionEnv,
		},
		{
			name:    "count not a number",
			env:     map[string]string{SegmentMaxCountEnv: "eight"},
			wantErr: "parse " + SegmentMaxCountEnv,
		},
		{
			name:    "count below range",
			env:     map[string]string{SegmentMaxCountEnv: "0"},
			wantErr: SegmentMaxCountEnv + " must be between 1 and 64, got 0",
		},
		{
			name:    "count above range",
			env:     map[string]string{SegmentMaxCountEnv: "65"},
			wantErr: SegmentMaxCountEnv + " must be between 1 and 64, got 65",
		},
		{
			name:    "chain percent not a number",
			env:     map[string]string{SegmentMaxChainPercentEnv: "half"},
			wantErr: "parse " + SegmentMaxChainPercentEnv,
		},
		{
			name:    "chain percent zero",
			env:     map[string]string{SegmentMaxChainPercentEnv: "0"},
			wantErr: SegmentMaxChainPercentEnv + " must be in (0, 100], got \"0\"",
		},
		{
			name:    "chain percent negative",
			env:     map[string]string{SegmentMaxChainPercentEnv: "-1"},
			wantErr: SegmentMaxChainPercentEnv + " must be in (0, 100], got \"-1\"",
		},
		{
			name:    "chain percent above range",
			env:     map[string]string{SegmentMaxChainPercentEnv: "100.1"},
			wantErr: SegmentMaxChainPercentEnv + " must be in (0, 100], got \"100.1\"",
		},
		{
			name:    "chain percent NaN",
			env:     map[string]string{SegmentMaxChainPercentEnv: "NaN"},
			wantErr: SegmentMaxChainPercentEnv + " must be in (0, 100], got \"NaN\"",
		},
		{
			name:    "chain percent Inf",
			env:     map[string]string{SegmentMaxChainPercentEnv: "Inf"},
			wantErr: SegmentMaxChainPercentEnv + " must be in (0, 100], got \"Inf\"",
		},
		{
			name:    "post percent not a number",
			env:     map[string]string{SegmentMaxPostPercentEnv: "half"},
			wantErr: "parse " + SegmentMaxPostPercentEnv,
		},
		{
			name:    "post percent zero",
			env:     map[string]string{SegmentMaxPostPercentEnv: "0"},
			wantErr: SegmentMaxPostPercentEnv + " must be in (0, 100], got \"0\"",
		},
		{
			name:    "post percent negative",
			env:     map[string]string{SegmentMaxPostPercentEnv: "-0.5"},
			wantErr: SegmentMaxPostPercentEnv + " must be in (0, 100], got \"-0.5\"",
		},
		{
			name:    "post percent above range",
			env:     map[string]string{SegmentMaxPostPercentEnv: "100.5"},
			wantErr: SegmentMaxPostPercentEnv + " must be in (0, 100], got \"100.5\"",
		},
		{
			name:    "post percent NaN",
			env:     map[string]string{SegmentMaxPostPercentEnv: "NaN"},
			wantErr: SegmentMaxPostPercentEnv + " must be in (0, 100], got \"NaN\"",
		},
		{
			name:    "invalid thresholds fail while disabled",
			env:     map[string]string{SegmentedCompactionEnv: "false", SegmentMaxCountEnv: "99"},
			wantErr: SegmentMaxCountEnv + " must be between 1 and 64, got 99",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(name string) (string, bool) {
				v, ok := tc.env[name]
				return v, ok
			}
			got, err := ParseSegmentedCompactionPolicy(lookup)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseSegmentedCompactionPolicy() error = nil, want %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseSegmentedCompactionPolicy() error = %q, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSegmentedCompactionPolicy() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("ParseSegmentedCompactionPolicy() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSegmentedCompactionEnvDistListsPolicyEnvNames pins .env.dist to the four
// policy variables and to the documented defaults.
func TestSegmentedCompactionEnvDistListsPolicyEnvNames(t *testing.T) {
	f, err := os.Open("../../.env.dist")
	if err != nil {
		t.Fatalf("open .env.dist: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Errorf("close .env.dist: %v", err)
		}
	}()

	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf(".env.dist line %q is not a NAME=value assignment", line)
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan .env.dist: %v", err)
	}

	wantNames := []string{
		SegmentedCompactionEnv,
		SegmentMaxCountEnv,
		SegmentMaxChainPercentEnv,
		SegmentMaxPostPercentEnv,
	}
	if len(values) != len(wantNames) {
		t.Fatalf(".env.dist defines %d variables, want exactly %d: %v", len(values), len(wantNames), values)
	}
	for _, name := range wantNames {
		if _, ok := values[name]; !ok {
			t.Errorf(".env.dist does not define %s", name)
		}
	}

	got, err := ParseSegmentedCompactionPolicy(func(name string) (string, bool) {
		v, ok := values[name]
		return v, ok
	})
	if err != nil {
		t.Fatalf("parse .env.dist values: %v", err)
	}
	if want := DefaultSegmentedCompactionPolicy(); got != want {
		t.Fatalf(".env.dist defaults parse to %+v, want %+v", got, want)
	}
}
