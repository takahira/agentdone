package claudedir

import (
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	t.Run("CLAUDE_CONFIG_DIR wins", func(t *testing.T) {
		cfg := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", cfg)
		t.Setenv("HOME", t.TempDir())
		got, err := Dir()
		if err != nil || got != cfg {
			t.Fatalf("Dir() = %q, %v; want %q", got, err, cfg)
		}
	})
	t.Run("falls back to ~/.claude", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		t.Setenv("HOME", home)
		got, err := Dir()
		want := filepath.Join(home, ".claude")
		if err != nil || got != want {
			t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("blank CLAUDE_CONFIG_DIR is treated as unset", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CLAUDE_CONFIG_DIR", "   ")
		t.Setenv("HOME", home)
		got, _ := Dir()
		if want := filepath.Join(home, ".claude"); got != want {
			t.Fatalf("Dir() = %q, want %q", got, want)
		}
	})
}
