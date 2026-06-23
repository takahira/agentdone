package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A non-git directory reports its base name and no branch; "" reports nothing.
func TestLocationNonGit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plaindir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := location(dir); got != "plaindir" {
		t.Errorf("location(non-git) = %q, want %q", got, "plaindir")
	}
	if got := location(""); got != "" {
		t.Errorf("location(\"\") = %q, want \"\"", got)
	}
}

// A git working tree reports its repo name and branch (exercises the timed git
// invocations in env.go's gitOut).
func TestLocationGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := filepath.Join(t.TempDir(), "myrepo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("symbolic-ref", "HEAD", "refs/heads/testbranch") // deterministic branch, no commit needed

	got := location(dir)
	if !strings.Contains(got, "myrepo") || !strings.Contains(got, "testbranch") {
		t.Errorf("location(git repo) = %q, want it to contain repo 'myrepo' and branch 'testbranch'", got)
	}
}
