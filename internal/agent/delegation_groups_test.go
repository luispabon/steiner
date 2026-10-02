package agent

import "testing"

func TestDelegationGroupLedgerClone(t *testing.T) {
	ledger := DelegationGroupLedger{Version: DelegationGroupLedgerVersion, Names: []string{"z", " a ", "a"}}
	got := ledger.Clone()
	if len(got.Names) != 2 || got.Names[0] != "a" || got.Names[1] != "z" {
		t.Fatalf("Clone names = %v", got.Names)
	}
	got.Names[0] = "changed"
	if ledger.Names[0] != "z" {
		t.Fatal("Clone aliases source names")
	}
}

func TestNormalizeDelegationGroup(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain", "a", "a"},
		{"trims", " \t a b\n", "a b"},
		{"blank", "  ", ""},
		{"empty", "", ""},
		{"case preserved", " Mixed ", "Mixed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeDelegationGroup(tt.in); got != tt.want {
				t.Fatalf("NormalizeDelegationGroup(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
