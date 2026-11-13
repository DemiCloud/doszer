package build

import (
	"testing"

	"github.com/DemiCloud/doszer/internal/manifest"
)

func TestValidateArchiveContent(t *testing.T) {
	tests := []struct {
		name    string
		model   manifest.Model
		paths   []string
		wantErr bool
	}{
		{"shell with WIN.INI", manifest.ModelShell, []string{"WINDOWS/WIN.INI"}, true},
		{"shell with a GRP file", manifest.ModelShell, []string{"WINDOWS/PROGRAM.GRP"}, true},
		{"shell without forbidden files", manifest.ModelShell, []string{"WINDOWS/WIN.COM"}, false},
		{"plain with WIN.INI is fine", manifest.ModelPlain, []string{"WINDOWS/WIN.INI"}, false},
		{"shell with DOSBOX.BAT", manifest.ModelShell, []string{"DOSBOX.BAT"}, true},
		{"OS image with DOSBOX.BAT", manifest.ModelOS, []string{"DOSBOX.BAT"}, true},
		{"plain with DOSBOX.BAT is fine", manifest.ModelPlain, []string{"DOSBOX.BAT"}, false},
		{"nested DOSBOX.BAT is fine", manifest.ModelShell, []string{"SUB/DOSBOX.BAT"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newTestSet(tt.paths...)
			err := validateArchiveContent(fs, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateArchiveContent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
