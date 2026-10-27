package builtin

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// legacyBashErrorCause is the string search bashErrorCause used before it read
// the cause from bashStreamError. It is the reference for cause parity.
func legacyBashErrorCause(msg string) string {
	for _, sep := range []string{"; stderr: ", "; stdout: "} {
		if i := strings.Index(msg, sep); i >= 0 {
			msg = msg[:i]
		}
	}
	return msg
}

// TestBashStreamReadErrorParity checks the bashStreamReadError message and the
// cause bashErrorCause reports for each combination of failed stream and
// captured text. execute only builds this error after a failed read, so every
// case sets at least one stream error.
func TestBashStreamReadErrorParity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		stdoutText string
		stdoutErr  error
		stderrText string
		stderrErr  error
		wantErr    string
		wantCause  string
	}{
		{name: "stdout error, no text", stdoutErr: io.EOF, wantErr: "bash session: read stdout: EOF", wantCause: "bash session: read stdout: EOF"},
		{name: "stdout error, stdout text", stdoutText: "out\n", stdoutErr: io.EOF, wantErr: "bash session: read stdout: EOF; stdout: out", wantCause: "bash session: read stdout: EOF"},
		{name: "stdout error, stderr text", stderrText: "err\n", stdoutErr: io.EOF, wantErr: "bash session: read stdout: EOF; stderr: err", wantCause: "bash session: read stdout: EOF"},
		{name: "stdout error, both text", stdoutText: "out\n", stderrText: "err\n", stdoutErr: io.EOF, wantErr: "bash session: read stdout: EOF; stderr: err; stdout: out", wantCause: "bash session: read stdout: EOF"},
		{name: "stderr error, no text", stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stderr: unexpected EOF", wantCause: "bash session: read stderr: unexpected EOF"},
		{name: "stderr error, stdout text", stdoutText: "out\n", stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stderr: unexpected EOF; stdout: out", wantCause: "bash session: read stderr: unexpected EOF"},
		{name: "stderr error, stderr text", stderrText: "err\n", stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stderr: unexpected EOF; stderr: err", wantCause: "bash session: read stderr: unexpected EOF"},
		{name: "stderr error, both text", stdoutText: "out\n", stderrText: "err\n", stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stderr: unexpected EOF; stderr: err; stdout: out", wantCause: "bash session: read stderr: unexpected EOF"},
		{name: "both errors, no text", stdoutErr: io.EOF, stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stdout: EOF; read stderr: unexpected EOF", wantCause: "bash session: read stdout: EOF; read stderr: unexpected EOF"},
		{name: "both errors, stdout text", stdoutText: "out\n", stdoutErr: io.EOF, stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stdout: EOF; read stderr: unexpected EOF; stdout: out", wantCause: "bash session: read stdout: EOF; read stderr: unexpected EOF"},
		{name: "both errors, stderr text", stderrText: "err\n", stdoutErr: io.EOF, stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stdout: EOF; read stderr: unexpected EOF; stderr: err", wantCause: "bash session: read stdout: EOF; read stderr: unexpected EOF"},
		{name: "both errors, both text", stdoutText: "out\n", stderrText: "err\n", stdoutErr: io.EOF, stderrErr: io.ErrUnexpectedEOF, wantErr: "bash session: read stdout: EOF; read stderr: unexpected EOF; stderr: err; stdout: out", wantCause: "bash session: read stdout: EOF; read stderr: unexpected EOF"},
		{name: "blank text is dropped", stdoutText: "  \n", stderrText: "\n", stdoutErr: io.EOF, wantErr: "bash session: read stdout: EOF", wantCause: "bash session: read stdout: EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bashStreamReadError(tt.stdoutText, tt.stdoutErr, tt.stderrText, tt.stderrErr)
			if got := err.Error(); got != tt.wantErr {
				t.Errorf("Error() = %q, want %q", got, tt.wantErr)
			}
			got := bashErrorCause(err)
			if want := legacyBashErrorCause(err.Error()); got != want {
				t.Errorf("bashErrorCause() = %q, legacy string search = %q", got, want)
			}
			if got != tt.wantCause {
				t.Errorf("bashErrorCause() = %q, want %q", got, tt.wantCause)
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				t.Error("errors.Is matched a stream read error; the stream errors are formatted into the message, not wrapped")
			}
		})
	}
}

// TestBashErrorCauseFallsBackToStringSearch covers errors that are not
// bashStreamError: bashErrorCause still cuts the message at the capture markers.
func TestBashErrorCauseFallsBackToStringSearch(t *testing.T) {
	t.Parallel()
	err := errors.New("bash session: write command: broken pipe; stdout: partial")
	if got, want := bashErrorCause(err), "bash session: write command: broken pipe"; got != want {
		t.Errorf("bashErrorCause() = %q, want %q", got, want)
	}
}
