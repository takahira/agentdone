package handler

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// location renders the "場所" value: repo name (and branch) for a git working
// tree, or the directory basename otherwise.
func location(cwd string) string {
	if cwd == "" {
		return ""
	}
	// repoAndBranch always yields a non-empty repo here: cwd is non-empty (guarded
	// above) and filepath.Base of a non-empty path never returns "".
	repo, branch := repoAndBranch(cwd)
	if branch != "" {
		if activeMessages().lang == "ja" {
			return repo + "（" + branch + "）"
		}
		return repo + " (" + branch + ")"
	}
	return repo
}

func repoAndBranch(cwd string) (repo, branch string) {
	if top := gitOut(cwd, "rev-parse", "--show-toplevel"); top != "" {
		repo = filepath.Base(top)
	} else {
		repo = filepath.Base(cwd)
	}
	branch = gitOut(cwd, "symbolic-ref", "--short", "-q", "HEAD")
	return repo, branch
}

func gitOut(cwd string, args ...string) string {
	// A hook must never hang Claude Code; cap git on a slow/stale mount.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", cwd}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
