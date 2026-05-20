package skill

import "testing"

func TestParseFrontmatter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		wantName string
		wantDesc string
	}{
		{
			name:     "with name and description",
			content:  "---\nname: plan\ndescription: Plan coding work.\n---\nBody text.",
			wantName: "plan",
			wantDesc: "Plan coding work.",
		},
		{
			name:     "without frontmatter",
			content:  "# Title\n\nBody text.",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "malformed yaml",
			content:  "---\nname: [unclosed bracket\n---\nBody.",
			wantName: "",
			wantDesc: "",
		},
		{
			name:     "empty description",
			content:  "---\nname: plan\ndescription:\n---\nBody.",
			wantName: "plan",
			wantDesc: "",
		},
		{
			name:     "description only",
			content:  "---\ndescription: Only a description.\n---\nBody.",
			wantName: "",
			wantDesc: "Only a description.",
		},
		{
			name:     "unclosed frontmatter",
			content:  "---\nname: plan\ndescription: foo\nNo closing fence.",
			wantName: "",
			wantDesc: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			name, desc := parseFrontmatter(tt.content)
			if name != tt.wantName {
				t.Errorf("parseFrontmatter() name = %q, want %q", name, tt.wantName)
			}
			if desc != tt.wantDesc {
				t.Errorf("parseFrontmatter() description = %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}
