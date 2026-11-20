package output

import (
	"strings"
	"testing"
)

func TestFormatCompactionDiagnosticsReportsRequestBlock(t *testing.T) {
	t.Parallel()

	withBlock := formatContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:          "compaction",
		Severity:      "done",
		RequestBlock:  RequestBlockNew,
		SummaryBytes:  120,
		RetainedUnits: 3,
	})
	if !strings.Contains(withBlock, "latest request kept verbatim (new)") {
		t.Fatalf("compaction summary = %q, want the request block note", withBlock)
	}

	dropped := formatContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:          "compaction",
		Severity:      "done",
		RequestBlock:  RequestBlockDropped,
		SummaryBytes:  120,
		RetainedUnits: 1,
	})
	if !strings.Contains(dropped, "latest request dropped to fit") {
		t.Fatalf("compaction summary = %q, want the dropped note", dropped)
	}
	if strings.Contains(dropped, "kept verbatim") {
		t.Fatalf("compaction summary = %q, want no kept-verbatim note for a dropped block", dropped)
	}

	withoutBlock := formatContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:         "compaction",
		Severity:     "done",
		RequestBlock: RequestBlockNone,
	})
	if strings.Contains(withoutBlock, "latest request") {
		t.Fatalf("compaction summary = %q, want no request block note", withoutBlock)
	}
}
