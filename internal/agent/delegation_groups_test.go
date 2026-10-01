package agent

import "testing"

func TestDelegationGroupLedgerCloneAndValidate(t *testing.T) {
	ledger := DelegationGroupLedger{Version: 1, Names: []string{"z", " a ", "a"}}
	if err := ledger.Validate(); err == nil {
		t.Fatal("Validate accepted noncanonical names")
	}
	got := ledger.Clone()
	if len(got.Names) != 2 || got.Names[0] != "a" || got.Names[1] != "z" {
		t.Fatalf("Clone names = %v", got.Names)
	}
	got.Names[0] = "changed"
	if ledger.Names[0] != "z" {
		t.Fatal("Clone aliases source names")
	}
	if err := (DelegationGroupLedger{Version: 1}).Validate(); err != nil {
		t.Fatalf("empty ledger invalid: %v", err)
	}
	if err := (DelegationGroupLedger{Version: 2}).Validate(); err == nil {
		t.Fatal("Validate accepted unsupported version")
	}
}
