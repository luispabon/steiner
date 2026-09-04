package tui

import "testing"

func TestHandleUpdateCheckResultMsg(t *testing.T) {
	tests := []struct {
		name              string
		msg               updateCheckResultMsg
		wantAvailable     bool
		wantLatestVersion string
	}{
		{
			name:              "update available sets sidebar fields",
			msg:               updateCheckResultMsg{latestVersion: "v9.9.9", available: true},
			wantAvailable:     true,
			wantLatestVersion: "v9.9.9",
		},
		{
			name:              "no update leaves sidebar fields at zero value",
			msg:               updateCheckResultMsg{latestVersion: "v9.9.9", available: false},
			wantAvailable:     false,
			wantLatestVersion: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{}
			m.handleUpdateCheckResultMsg(tc.msg)
			if m.sidebar.updateAvailable != tc.wantAvailable {
				t.Errorf("sidebar.updateAvailable = %v, want %v", m.sidebar.updateAvailable, tc.wantAvailable)
			}
			if m.sidebar.latestVersion != tc.wantLatestVersion {
				t.Errorf("sidebar.latestVersion = %q, want %q", m.sidebar.latestVersion, tc.wantLatestVersion)
			}
		})
	}
}
