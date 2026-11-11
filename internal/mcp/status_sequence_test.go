package mcp

import (
	"context"
	"errors"
	"testing"
)

// TestStatusReportSequenceOrdersTransitions pins the session half of the
// ordering fix: every status transition is sequenced under the session mutex, in
// transition order, so a report delivered late still says which transition it
// describes. Status reporting itself stays outside the mutex.
func TestStatusReportSequenceOrdersTransitions(t *testing.T) {
	current := newInMemoryClientSession(t)
	next := newInMemoryClientSession(t)

	var (
		s         *Session
		delivered []StatusReport
	)
	s = &Session{
		name:       "fake",
		sdk:        current,
		managerCtx: context.Background(),
		onStatus: func(report StatusReport) {
			if !s.mu.TryLock() {
				t.Error("reportStatus ran with the session mutex held")
				return
			}
			s.mu.Unlock()
			delivered = append(delivered, report)
		},
	}

	// The worker's steps for one successful attempt, then one that ends
	// unavailable.
	first := beginTestReconnect(t, s)
	s.reportStatus(first.begin)
	_, committedReport, committed := s.commitReconnect(first, next, nil)
	if !committed {
		t.Fatal("commitReconnect refused a live session")
	}
	s.reportStatus(committedReport)

	second := beginTestReconnect(t, s)
	s.reportStatus(second.begin)
	attemptErr := errors.New("dial failed")
	s.resolveReconnect(second, attemptErr, ServerStatusUnavailable)

	wantStatuses := []ServerStatus{
		ServerStatusReconnecting,
		ServerStatusConnected,
		ServerStatusReconnecting,
		ServerStatusUnavailable,
	}
	if len(delivered) != len(wantStatuses) {
		t.Fatalf("delivered %d reports, want %d: %+v", len(delivered), len(wantStatuses), delivered)
	}
	for i, report := range delivered {
		if report.Status != wantStatuses[i] {
			t.Errorf("report %d status = %q, want %q", i, report.Status, wantStatuses[i])
		}
		if report.Seq != uint64(i+1) {
			t.Errorf("report %d sequence = %d, want %d", i, report.Seq, i+1)
		}
	}
	if !errors.Is(delivered[3].Err, attemptErr) {
		t.Errorf("unavailable report error = %v, want %v", delivered[3].Err, attemptErr)
	}
	for _, report := range delivered[:3] {
		if report.Err != nil {
			t.Errorf("report %q error = %v, want nil", report.Status, report.Err)
		}
	}
}

// TestManagerDropsStaleReportAfterNewerUnavailable is the regression for the
// gated probe: a report delivered outside the session mutex can arrive after a
// newer transition already landed. The older connected report must not
// supersede the newer unavailable state.
func TestManagerDropsStaleReportAfterNewerUnavailable(t *testing.T) {
	current := newInMemoryClientSession(t)
	next := newInMemoryClientSession(t)

	var delivered []StatusReport
	s := &Session{
		name:       "fake",
		sdk:        current,
		managerCtx: context.Background(),
		onStatus:   func(report StatusReport) { delivered = append(delivered, report) },
	}

	// Attempt 1 reconnects successfully, attempt 2 ends unavailable.
	first := beginTestReconnect(t, s)
	s.reportStatus(first.begin)
	_, connected, committed := s.commitReconnect(first, next, nil)
	if !committed {
		t.Fatal("commitReconnect refused a live session")
	}
	second := beginTestReconnect(t, s)
	s.reportStatus(second.begin)
	s.resolveReconnect(second, errors.New("dial failed"), ServerStatusUnavailable)

	m := &Manager{}
	m.setServerState(ServerState{Name: "fake", Status: ServerStatusConnecting, Transport: "stdio"})
	changes := 0
	onChange := func() { changes++ }
	for _, report := range delivered {
		m.updateServerStatus("fake", report, onChange)
	}
	if changes != 2 {
		t.Fatalf("state changes = %d, want 2 (reconnecting, unavailable)", changes)
	}

	// The first attempt's connected report arrives late.
	m.updateServerStatus("fake", connected, onChange)

	if st := stateByName(m.ServerStates(), "fake"); st == nil || st.Status != ServerStatusUnavailable {
		t.Fatalf("state after a delayed connected report = %+v, want unavailable", st)
	}
	if changes != 2 {
		t.Errorf("state changes = %d, want 2 (the stale report emits nothing)", changes)
	}
}

// TestManagerSameStatusReportAdvancesSequence proves a newer report repeating
// the current status keeps the same-status notification behaviour (no
// onStateChange) while still advancing the applied sequence, so an older
// transition delivered afterwards is dropped.
func TestManagerSameStatusReportAdvancesSequence(t *testing.T) {
	m := &Manager{}
	m.setServerState(ServerState{Name: "fake", Status: ServerStatusReconnecting, Transport: "stdio"})
	changes := 0
	onChange := func() { changes++ }

	m.updateServerStatus("fake", StatusReport{Seq: 2, Status: ServerStatusConnected}, onChange)
	if st := stateByName(m.ServerStates(), "fake"); st == nil || st.Status != ServerStatusConnected {
		t.Fatalf("state after the connected report = %+v, want connected", st)
	}
	if changes != 1 {
		t.Fatalf("state changes = %d, want 1", changes)
	}

	m.updateServerStatus("fake", StatusReport{Seq: 4, Status: ServerStatusConnected}, onChange)
	if changes != 1 {
		t.Errorf("a repeated status report emitted onStateChange (%d changes)", changes)
	}
	m.updateServerStatus("fake", StatusReport{Seq: 3, Status: ServerStatusUnavailable}, onChange)

	if st := stateByName(m.ServerStates(), "fake"); st == nil || st.Status != ServerStatusConnected {
		t.Errorf("state after an older report = %+v, want connected", st)
	}
	if changes != 1 {
		t.Errorf("state changes = %d, want 1", changes)
	}
}

// TestManagerAppliesNewerStatusReports covers normal behaviour: every newer
// sequence is applied in order, the failure text follows the report, and each
// changed status notifies once.
func TestManagerAppliesNewerStatusReports(t *testing.T) {
	m := &Manager{}
	m.setServerState(ServerState{Name: "fake", Status: ServerStatusConnected, Transport: "stdio"})
	changes := 0
	onChange := func() { changes++ }

	dialErr := errors.New("dial failed")
	steps := []struct {
		report  StatusReport
		wantErr string
	}{
		{StatusReport{Seq: 1, Status: ServerStatusReconnecting}, ""},
		{StatusReport{Seq: 2, Status: ServerStatusUnavailable, Err: dialErr}, dialErr.Error()},
		{StatusReport{Seq: 3, Status: ServerStatusReconnecting}, ""},
		{StatusReport{Seq: 4, Status: ServerStatusConnected}, ""},
	}
	for i, step := range steps {
		m.updateServerStatus("fake", step.report, onChange)
		st := stateByName(m.ServerStates(), "fake")
		if st == nil || st.Status != step.report.Status {
			t.Fatalf("step %d state = %+v, want %q", i, st, step.report.Status)
		}
		if st.Err != step.wantErr {
			t.Errorf("step %d error = %q, want %q", i, st.Err, step.wantErr)
		}
		if changes != i+1 {
			t.Fatalf("step %d state changes = %d, want %d", i, changes, i+1)
		}
	}
}

// TestManagerDropsStaleReportAfterClose proves the sequence gate also applies
// after the manager closes: a delayed older transition cannot publish over the
// newer state recorded before shutdown.
func TestManagerDropsStaleReportAfterClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{ctx: ctx, cancel: cancel}
	m.setServerState(ServerState{Name: "fake", Status: ServerStatusConnected, Transport: "stdio"})
	changes := 0
	onChange := func() { changes++ }

	m.updateServerStatus("fake", StatusReport{Seq: 2, Status: ServerStatusUnavailable, Err: errors.New("dial failed")}, onChange)
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m.updateServerStatus("fake", StatusReport{Seq: 1, Status: ServerStatusConnected}, onChange)

	if st := stateByName(m.ServerStates(), "fake"); st == nil || st.Status != ServerStatusUnavailable {
		t.Fatalf("state after Close and a delayed connected report = %+v, want unavailable", st)
	}
	if changes != 1 {
		t.Errorf("state changes = %d, want 1 (the stale report emits nothing)", changes)
	}
}
