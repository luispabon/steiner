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

	withoutBlock := formatContextDiagnosticsEvent(ContextDiagnosticsEvent{
		Kind:         "compaction",
		Severity:     "done",
		RequestBlock: RequestBlockNone,
	})
	if strings.Contains(withoutBlock, "latest request kept verbatim") {
		t.Fatalf("compaction summary = %q, want no request block note", withoutBlock)
	}
}
