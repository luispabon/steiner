package patchdoc

import "testing"

func TestHunkAffectedPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hunk Hunk
		want string
	}{
		{
			name: "add file",
			hunk: AddFile{PathValue: "new.txt"},
			want: "new.txt",
		},
		{
			name: "delete file",
			hunk: DeleteFile{PathValue: "old.txt"},
			want: "old.txt",
		},
		{
			name: "update file without move",
			hunk: UpdateFile{PathValue: "src.txt"},
			want: "src.txt",
		},
		{
			name: "update file with move",
			hunk: UpdateFile{PathValue: "src.txt", MovePath: "dst.txt"},
			want: "dst.txt",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.hunk.AffectedPath(); got != tt.want {
				t.Fatalf("AffectedPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
