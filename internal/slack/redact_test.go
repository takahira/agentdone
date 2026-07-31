package slack

import (
	"os"
	"strings"
	"testing"
)

// A credential that reaches Post() must never appear in what is sent. These are the
// shapes seen in real prompts and assistant summaries.
func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
		keep   []string // readable context that must survive
	}{
		{"env assignment", "deploy with GITHUB_TOKEN=ghp_FAKEabc123456789 to prod",
			"ghp_FAKEabc123456789", []string{"GITHUB_TOKEN", "deploy", "prod"}},
		{"lowercase password", "set db_password=Hunter2Passwd now", "Hunter2Passwd", []string{"db_password"}},
		{"semicolon inside value", "run with PASSWORD=abc;def please", "abc;def", []string{"PASSWORD", "please"}},
		{"quoted value with spaces", `export API_KEY="a b c secret"`, "a b c secret", []string{"API_KEY"}},
		{"long flag", "tool --password sekret --port 1", "sekret", []string{"--password", "--port 1"}},
		{"long flag equals", "tool --token=tok_live_99 run", "tok_live_99", []string{"--token", "run"}},
		{"authorization header", `curl -H "Authorization: Bearer sk-live-FAKE123SECRET" https://api.example.com`,
			"sk-live-FAKE123SECRET", []string{"Authorization", "Bearer", "api.example.com"}},
		{"url userinfo", "psql postgres://user:s3cr3tpw@host:5432/db", "s3cr3tpw",
			[]string{"postgres://user", "host:5432/db"}},
		{"mysql short flag", "mysql -uroot -pHunter2 db", "Hunter2", []string{"mysql", "-uroot", "db"}},
		{"redis short flag", "redis-cli -h 10.0.0.1 -a Sup3rSecret ping", "Sup3rSecret",
			[]string{"redis-cli", "10.0.0.1", "ping"}},
		{"sshpass", "sshpass -pMyPass ssh user@host", "MyPass", []string{"sshpass", "ssh user@host"}},
		{"curl basic auth", "curl -u admin:hunter2 https://x.test", "hunter2", []string{"admin", "x.test"}},
		{"github token bare", "the key is ghp_FAKEabcdefghijklmnop ok", "ghp_FAKEabcdefghijklmnop", []string{"the key is"}},
		{"aws access key id", "using AKIAIOSFODNN7EXAMPLE here", "AKIAIOSFODNN7EXAMPLE", []string{"using", "here"}},
		{"slack token", "token xoxb-1234567890-abcdefg set", "xoxb-1234567890-abcdefg", []string{"set"}},
		{"anthropic key", "ANTHROPIC_API_KEY=sk-ant-FAKE1234567890abcd done",
			"sk-ant-FAKE1234567890abcd", []string{"ANTHROPIC_API_KEY", "done"}},
		{"slack webhook bare url", "https://hooks.slack.com/services/T0FAKE/B0FAKE/FAKEabcdef1234567890tokn",
			"T0FAKE/B0FAKE/FAKEabcdef1234567890tokn", []string{"hooks.slack.com/services/"}},
		{"slack webhook kv", "SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T0FAKE/B0FAKE/FAKEtok123 done",
			"FAKEtok123", []string{"SLACK_WEBHOOK_URL", "hooks.slack.com/services/", "done"}},
		{"slack webhook in prose with query", "post it to https://hooks.slack.com/services/T0FAKE/B0FAKE/FAKEtoken99?x=1 please",
			"FAKEtoken99", []string{"post it to", "hooks.slack.com/services/", "?x=1", "please"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := RedactSecrets(c.in)
			if strings.Contains(out, c.secret) {
				t.Fatalf("secret survived redaction\n in: %s\nout: %s", c.in, out)
			}
			if !strings.Contains(out, mask) {
				t.Fatalf("nothing was masked\n in: %s\nout: %s", c.in, out)
			}
			for _, k := range c.keep {
				if !strings.Contains(out, k) {
					t.Errorf("lost readable context %q\n in: %s\nout: %s", k, c.in, out)
				}
			}
		})
	}
}

// Ordinary text must survive untouched, or every notification becomes unreadable.
func TestRedactSecretsLeavesProseAlone(t *testing.T) {
	for _, s := range []string{
		"Fixed the login bug and pushed to main.",
		"docker run -p 8080:80 nginx",
		"psql -p 5432 mydb",
		"Renamed auth.go to session.go",
		"The auth flow now returns 401 on expiry.",
		"npm run build -- --mode production",
	} {
		if got := RedactSecrets(s); got != s {
			t.Errorf("prose was altered\n in: %s\nout: %s", s, got)
		}
	}
}

func TestRedactSecretsPEMBlock(t *testing.T) {
	in := "here is the key\n-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234\nabcd\n-----END RSA PRIVATE KEY-----\nthanks"
	out := RedactSecrets(in)
	if strings.Contains(out, "MIIEowIBAAKCAQEA1234") {
		t.Fatalf("PEM body survived: %s", out)
	}
	if !strings.Contains(out, "here is the key") || !strings.Contains(out, "thanks") {
		t.Fatalf("surrounding prose lost: %s", out)
	}
}

// Post() is the single egress point; redaction must apply there, not only at the
// call sites, so no future caller can bypass it.
func TestPostRedactsBeforeSending(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	err = Post("", "プロンプト：deploy with GITHUB_TOKEN=ghp_FAKEabc123456789 to prod")
	os.Stdout = orig
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	got := string(buf[:n])
	if strings.Contains(got, "ghp_FAKEabc123456789") {
		t.Fatalf("credential reached the wire: %s", got)
	}
	// Post() escapes after redacting, so the mask arrives entity-encoded. Slack
	// renders it back to "<redacted>"; assert the escaped form that is actually sent.
	if !strings.Contains(got, escape(mask)) {
		t.Fatalf("expected a mask in the payload: %s", got)
	}
}
