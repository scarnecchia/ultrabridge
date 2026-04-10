package supernote

import (
	"encoding/json"
	"testing"
)

func TestConfigUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    Config
		wantErr bool
	}{
		{
			name:    "valid config",
			jsonStr: `{"notes_path":"/var/notes","backup_path":"/var/backup"}`,
			want: Config{
				NotesPath:  "/var/notes",
				BackupPath: "/var/backup",
			},
			wantErr: false,
		},
		{
			name:    "minimal config",
			jsonStr: `{"notes_path":"/var/notes"}`,
			want: Config{
				NotesPath:  "/var/notes",
				BackupPath: "",
			},
			wantErr: false,
		},
		{
			name:    "empty config",
			jsonStr: `{}`,
			want: Config{
				NotesPath:  "",
				BackupPath: "",
			},
			wantErr: false,
		},
		{
			name:    "extra unknown fields ignored",
			jsonStr: `{"notes_path":"/var/notes","backup_path":"/var/backup","unknown_field":"value"}`,
			want: Config{
				NotesPath:  "/var/notes",
				BackupPath: "/var/backup",
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			jsonStr: `{invalid json}`,
			want:    Config{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := json.Unmarshal([]byte(tt.jsonStr), &cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && cfg != tt.want {
				t.Errorf("Unmarshal() got %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

func TestConfigMarshal(t *testing.T) {
	cfg := Config{
		NotesPath:  "/var/notes",
		BackupPath: "/var/backup",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded != cfg {
		t.Errorf("Round-trip failed: got %+v, want %+v", decoded, cfg)
	}
}
