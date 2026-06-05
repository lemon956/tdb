package app

import "testing"

func TestClipboardCommandCandidatesByOS(t *testing.T) {
	tests := []struct {
		goos string
		want []clipboardCommand
	}{
		{
			goos: "darwin",
			want: []clipboardCommand{{Name: "pbcopy"}},
		},
		{
			goos: "linux",
			want: []clipboardCommand{
				{Name: "wl-copy"},
				{Name: "xclip", Args: []string{"-selection", "clipboard"}},
				{Name: "xsel", Args: []string{"--clipboard", "--input"}},
			},
		},
		{
			goos: "windows",
			want: []clipboardCommand{
				{Name: "clip.exe"},
				{Name: "powershell.exe", Args: []string{"-NoProfile", "-Command", "Set-Clipboard"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			got := clipboardCommandCandidates(tt.goos)
			if len(got) != len(tt.want) {
				t.Fatalf("candidate count = %d, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i].Name != tt.want[i].Name {
					t.Fatalf("candidate[%d].Name = %q, want %q", i, got[i].Name, tt.want[i].Name)
				}
				if !stringSlicesEqual(got[i].Args, tt.want[i].Args) {
					t.Fatalf("candidate[%d].Args = %#v, want %#v", i, got[i].Args, tt.want[i].Args)
				}
			}
		})
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
