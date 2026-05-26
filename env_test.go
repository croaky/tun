package tun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnv(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		env      map[string]string // pre-existing env vars
		wantEnv  map[string]string // expected env vars after load
		wantSkip map[string]bool   // keys that should NOT be set
	}{
		{
			name:    "basic TUN vars",
			content: "TUN_SERVER=ws://localhost\nTUN_TOKEN=secret",
			wantEnv: map[string]string{
				"TUN_SERVER": "ws://localhost",
				"TUN_TOKEN":  "secret",
			},
		},
		{
			name:    "PORT is allowed",
			content: "PORT=3000",
			wantEnv: map[string]string{"PORT": "3000"},
		},
		{
			name:    "non TUN vars ignored",
			content: "OTHER_VAR=value\nTUN_TOKEN=secret",
			wantEnv: map[string]string{"TUN_TOKEN": "secret"},
			wantSkip: map[string]bool{
				"OTHER_VAR": true,
			},
		},
		{
			name:    "existing env not overwritten",
			content: "TUN_TOKEN=new",
			env:     map[string]string{"TUN_TOKEN": "existing"},
			wantEnv: map[string]string{"TUN_TOKEN": "existing"},
		},
		{
			name:    "quoted values stripped",
			content: "TUN_TOKEN=\"quoted\"\nTUN_SERVER='single'",
			wantEnv: map[string]string{
				"TUN_TOKEN":  "quoted",
				"TUN_SERVER": "single",
			},
		},
		{
			name:    "export prefix handled",
			content: "export TUN_TOKEN=secret",
			wantEnv: map[string]string{"TUN_TOKEN": "secret"},
		},
		{
			name:    "comments and blank lines skipped",
			content: "# comment\n\nTUN_TOKEN=secret\n",
			wantEnv: map[string]string{"TUN_TOKEN": "secret"},
		},
		{
			name:    "whitespace trimmed",
			content: "  TUN_TOKEN  =  secret  ",
			wantEnv: map[string]string{"TUN_TOKEN": "secret"},
		},
		{
			name: "multiline quoted non TUN value does not break parsing",
			content: "CODE_STORAGE_API_KEY=\"-----BEGIN PRIVATE KEY-----\n" +
				"line2\n" +
				"line3\n" +
				"-----END PRIVATE KEY-----\"\n" +
				"TUN_TOKEN=secret\n",
			wantEnv: map[string]string{"TUN_TOKEN": "secret"},
			wantSkip: map[string]bool{
				"CODE_STORAGE_API_KEY": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, ".env")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}

			for k := range tt.wantEnv {
				_ = os.Unsetenv(k)
			}
			for k := range tt.wantSkip {
				_ = os.Unsetenv(k)
			}
			for k, v := range tt.env {
				_ = os.Setenv(k, v)
			}

			Load(path)

			for k, want := range tt.wantEnv {
				if got := os.Getenv(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}

			for k := range tt.wantSkip {
				if got := os.Getenv(k); got != "" {
					t.Errorf("%s should not be set, got %q", k, got)
				}
			}
		})
	}
}

func TestLoadEnv_FileNotFound(t *testing.T) {
	Load("/nonexistent/path/.env")
}
