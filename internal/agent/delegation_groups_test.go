package agent

import "testing"

func TestDelegationGroupLedgerClone(t *testing.T) {
	ledger := DelegationGroupLedger{Version: 1, Names: []string{"z", " a ", "a"}}
	got := ledger.Clone()
	if len(got.Names) != 2 || got.Names[0] != "a" || got.Names[1] != "z" {
		t.Fatalf("Clone names = %v", got.Names)
	}
	got.Names[0] = "changed"
	if ledger.Names[0] != "z" {
		t.Fatal("Clone aliases source names")
	}
}
