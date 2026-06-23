package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"

	"github.com/takahira/agentdone/internal/config"
	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/pkg/cchooks"
)

// Doctor prints a self-diagnosis — hook wiring, webhook, state dir, language —
// without sending anything. It exists because the hook schema this tool reads
// is reverse-engineered, not a published API: when notifications misbehave
// after a Claude Code update, this is the first thing to run. Returns an error
// (non-zero exit) when notifications cannot currently work.
func Doctor(w io.Writer) error {
	healthy := true

	path, err := settingsPath()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "settings: %s\n", path)
	if _, hooks, lerr := loadSettings(path); lerr != nil {
		fmt.Fprintf(w, "  ✗ unreadable: %v\n", lerr)
		healthy = false
	} else {
		missing := map[string]bool{} // wired command -> binary gone (dedup across events)
		for _, wi := range wirings() {
			cmds := wiredCommands(hooks[wi.event])
			if len(cmds) == 0 {
				fmt.Fprintf(w, "  ✗ %s not wired (run: agentdone init)\n", wi.event)
				healthy = false
				continue
			}
			fmt.Fprintf(w, "  ✓ %s wired\n", wi.event)
			for _, c := range cmds {
				if !missing[c] && !commandExists(c) {
					missing[c] = true
				}
			}
		}
		// Settings can outlive the binary they point at (a cleaned ~/.claude/bin,
		// a removed `go install`): every hook then dies with exit 127 while the
		// wiring still reads as present, so check the command actually resolves.
		for c := range missing {
			fmt.Fprintf(w, "  ✗ wired command not found: %s (run `init` from a current binary)\n", c)
			healthy = false
		}
	}

	switch url, werr := config.ResolveWebhook(); {
	case werr != nil:
		fmt.Fprintf(w, "webhook: ✗ %v\n", werr)
		healthy = false
	case url == "":
		fmt.Fprintln(w, "webhook: ✗ not configured (set SLACK_WEBHOOK_URL or ~/.claude/hooks/.webhook)")
		healthy = false
	default:
		fmt.Fprintln(w, "webhook: ✓ configured (not contacted)")
	}

	// Probe the state dir the same way a real turn would.
	if serr := state.Save("doctor-probe", state.Turn{StartEpoch: 1}); serr != nil {
		fmt.Fprintf(w, "state:   ✗ %v\n", serr)
		healthy = false
	} else {
		state.Delete("doctor-probe")
		fmt.Fprintln(w, "state:   ✓ writable")
	}

	fmt.Fprintf(w, "language: %s\n", activeMessages().lang)
	// Show the effective threshold: an invalid AGENTDONE_THRESHOLD ("30m", a
	// negative) silently falls back to the default, so surface what is actually
	// in force (and a "0s" tells the user every completion will notify).
	fmt.Fprintf(w, "threshold: %ds\n", thresholdSeconds())
	fmt.Fprintf(w, "hook schema verified against claude-code %s\n", cchooks.VerifiedClaudeCodeVersion)

	if !healthy {
		return errors.New("doctor found problems (see above)")
	}
	return nil
}

// wiredCommands returns the commands of our hook entries across the event's
// matchers (empty when nothing of ours is wired).
func wiredCommands(matchers []json.RawMessage) []string {
	var cmds []string
	for _, raw := range matchers {
		var m struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for _, h := range m.Hooks {
			if isOurs(h.Command) {
				cmds = append(cmds, h.Command)
			}
		}
	}
	return cmds
}

// commandExists reports whether the executable a wired hook command points at
// still resolves to something that can actually run. A bare name is looked up
// on PATH; anything with a path separator is stat'ed (init writes forward
// slashes, which os.Stat accepts on Windows too).
//
// Two refinements over a bare stat: (1) a directory or a regular file without an
// execute bit stats fine but spawns with EACCES/ENOEXEC at hook time, so it is
// NOT a working command; (2) a manually wired shell-form hook may use ~ / $HOME
// / env vars, which the shell expands at runtime — expand them the same way
// before stat'ing so a working hook isn't falsely reported "not found".
func commandExists(command string) bool {
	c := executableOf(command)
	if c == "" {
		return false
	}
	if !strings.ContainsAny(c, `/\`) {
		_, err := exec.LookPath(c)
		return err == nil
	}
	fi, err := os.Stat(expandPath(c))
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		return false
	}
	return true
}

// expandPath resolves a leading ~ and a $HOME / ${HOME} reference the way a
// shell would before a shell-form hook runs, so commandExists checks the path
// that would actually be executed. Exec-form hooks (what init writes) carry no
// ~ or $, so this is a no-op for them.
//
// Only $HOME/${HOME} are expanded, NOT arbitrary $VAR via os.ExpandEnv: a real
// install path may legitimately contain a literal '$', which ExpandEnv would
// blank out (turning a working hook into a phantom "not found").
func expandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, herr := os.UserHomeDir(); herr == nil {
			p = home + strings.TrimPrefix(p, "~")
		}
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		p = homeVarRe.ReplaceAllStringFunc(p, func(m string) string {
			// ${HOME} and $HOME both map to home; preserve a trailing path
			// separator captured to keep $HOME apart from $HOMEBREW_PREFIX etc.
			if n := len(m); n > 0 && (m[n-1] == '/' || m[n-1] == '\\') {
				return home + m[n-1:]
			}
			return home
		})
	}
	return p
}

// homeVarRe matches a $HOME / ${HOME} reference only at a variable boundary: a
// trailing path separator or end of string. strings.ReplaceAll("$HOME") would
// also rewrite the "$HOME" prefix of $HOMEBREW_PREFIX / $HOMEDIR and corrupt the
// path; this does not.
var homeVarRe = regexp.MustCompile(`\$\{HOME\}|\$HOME($|[/\\])`)
