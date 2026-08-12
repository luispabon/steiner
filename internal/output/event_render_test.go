package output

import "testing"

func TestRenderConfigWarningEvent(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "plain message",
			message: "project_context.max_tokens is deprecated",
			want:    "project_context.max_tokens is deprecated",
		},
		{
			name:    "empty message",
			message: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seg := renderEvent(NewConfigWarningEvent(tt.message))
			if seg.Channel != ChannelStatus {
				t.Fatalf("Channel = %q, want %q", seg.Channel, ChannelStatus)
			}
			if seg.Label != "status" {
				t.Fatalf("Label = %q, want %q", seg.Label, "status")
			}
			if seg.Text != tt.want {
				t.Fatalf("Text = %q, want %q", seg.Text, tt.want)
			}
		})
	}
}
