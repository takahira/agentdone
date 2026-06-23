package handler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExpandPath confirms ~ and $HOME expand, but an arbitrary $VAR in a real
// path is left literal (os.ExpandEnv would blank it and break a working hook),
// and a $HOME prefix of another variable ($HOMEBREW_PREFIX) is NOT rewritten.
func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := map[string]string{
		"~/bin/agentdone":       home + "/bin/agentdone",
		"$HOME/bin/agentdone":   home + "/bin/agentdone",
		"${HOME}/bin/agentdone": home + "/bin/agentdone",
		"$HOME":                 home,                    // bare $HOME at end of string
		"/opt/$weird/agentdone": "/opt/$weird/agentdone", // arbitrary $VAR stays literal
		"$HOMEBREW_PREFIX/bin":  "$HOMEBREW_PREFIX/bin",  // $HOME prefix of another var, untouched
		"$HOMEDIR/x":            "$HOMEDIR/x",            // not a $HOME boundary
		"/usr/local/bin/x":      "/usr/local/bin/x",
	}
	for in, want := range cases {
		if got := expandPath(in); got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCommandExistsResolves confirms a wired hook command is "found" only when
// it resolves to something runnable: a directory or a non-executable file is
// not, and ~ / $HOME paths are expanded before stat'ing.
func TestCommandExistsResolves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("execute-bit semantics are Unix-only")
	}
	d := t.TempDir()

	dirPath := filepath.Join(d, "asdir")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if commandExists(dirPath) {
		t.Errorf("commandExists(directory) = true, want false")
	}

	noExec := filepath.Join(d, "plain")
	if err := os.WriteFile(noExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if commandExists(noExec) {
		t.Errorf("commandExists(non-executable) = true, want false")
	}
	if err := os.Chmod(noExec, 0o755); err != nil {
		t.Fatal(err)
	}
	if !commandExists(noExec) {
		t.Errorf("commandExists(executable) = false, want true")
	}

	// A ~- / $HOME-prefixed manual wiring that resolves under HOME must be recognised.
	home := t.TempDir()
	t.Setenv("HOME", home)
	binDir := filepath.Join(home, "tools")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "agentdone"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !commandExists("~/tools/agentdone") {
		t.Errorf("commandExists(~/tools/agentdone) = false, want true")
	}
	if !commandExists("$HOME/tools/agentdone") {
		t.Errorf("commandExists($HOME/tools/agentdone) = false, want true")
	}
}

// TestCommandExistsDollarPathNotMangled confirms a real install path containing
// a literal '$' resolves (os.ExpandEnv would have blanked it).
func TestCommandExistsDollarPathNotMangled(t *testing.T) {
	d := t.TempDir()
	dir := filepath.Join(d, "we$rd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "agentdone")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !commandExists(bin) {
		t.Errorf("commandExists(%q) = false, want true (literal $ must not be expanded away)", bin)
	}
}
