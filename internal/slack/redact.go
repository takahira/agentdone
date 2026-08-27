package slack

import (
	"regexp"
	"strings"
)

// Inline-secret masking for the notification body.
//
// Most of a notification is transcript-derived: the user's prompt, the assistant's
// closing summary, a question, an error string. Any of those can carry a credential
// the user pasted or a command they asked to run, and unlike the rest of this tool's
// state that text LEAVES THE MACHINE -- Slack stores it, indexes it for search, and
// shows it to everyone in the channel. Masking happens in Post(), the single egress
// point, so no call site can bypass it.
//
// The rule set mirrors agent-trail's `_apply_secret_subs`, which was written against
// the same threat and is the reference implementation. Matching is name/shape based;
// content sniffing is deliberately avoided.
//
// Trade-off: these patterns are deliberately eager. Over-masking costs a little
// readability in a notification, under-masking publishes a credential -- so when a
// value sits behind a credential-shaped key, it is masked.

const mask = "<redacted>"

var (
	// KEY=VALUE / KEY: VALUE where the key looks credential-shaped. Optional JSON or
	// YAML quotes around the key are included in group 1, which keeps the key and
	// delimiter. The value runs to whitespace, or to the closing quote when quoted,
	// so `API_KEY="a b c"` does not leak its tail. `;` is deliberately part
	// of the value: `PASSWORD=abc;def` may be a real password containing a
	// semicolon, and leaving `;def` behind publishes its tail. The cost is eager
	// masking of text glued to the value (`TOKEN=x;echo hi` masks `x;echo`, a
	// connection string loses the segment after the password) — per the trade-off
	// above, readability loss beats a partial credential. `|` and `&` still stop
	// the value: unquoted passwords containing them are rare, and space-less shell
	// pipelines (`TOKEN=x|grep`) are not.
	kvSecretRe = regexp.MustCompile(
		`(?i)(["']?[A-Za-z0-9_.-]*(?:passwd|password|secret|token|api[_-]?key|access[_-]?key|auth|credential|private[_-]?key)[A-Za-z0-9_.-]*["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s|&]+)`)

	// Long flags: --password VALUE / --token=VALUE. Group 1 keeps the flag+delimiter.
	flagSecretRe = regexp.MustCompile(
		`(?i)(--(?:password|passwd|token|secret|api-?key|access-?key|auth|credential)(?:=|\s+))("[^"]*"|'[^']*'|[^\s;|&]+)`)

	// Authorization: <scheme> <credential> -- keep the scheme, mask the credential.
	authHeaderRe = regexp.MustCompile(`(?i)(Authorization:\s*(?:Bearer|Basic|Token|Digest)\s+)([^\s"';|&]+)`)

	// Bare `Bearer <token>` outside an Authorization header.
	bearerRe = regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9._~+/-]{12,}=*)`)

	// scheme://user:pass@host -- mask only the password, keep user and host so the
	// notification still says what was contacted.
	urlCredRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:@/]*:)([^\s@/]+)(@)`)

	// sshpass -p<pass>, and the DB/redis short flags where the letter is unambiguously
	// a password. Scoped to those tools on purpose: `docker -p` is a port and
	// `psql -p` is a port. Group 1 keeps everything up to and including the flag.
	sshpassRe = regexp.MustCompile(`(?i)(\bsshpass\b[^\n]{0,120}?\s-p)\s*([^\s;|&]+)`)
	dbPassRe  = regexp.MustCompile(`(?i)(\b(?:mysql|mysqldump|mariadb|mongosh)\b[^\n]{0,300}?\s-p)\s*([A-Za-z0-9][^\s;|&]*)`)
	redisRe   = regexp.MustCompile(`(?i)(\bredis-cli\b[^\n]{0,300}?\s-a)\s*([A-Za-z0-9][^\s;|&]*)`)

	// curl basic auth: -u user:pass / --user user:pass. Require the colon shape so a
	// bare -u in another tool is not over-masked. Group 1 keeps the flag and user.
	userPassRe = regexp.MustCompile(`(?i)((?:^|\s)(?:-u\s*|--user[=\s]\s*)[^\s:;|&]+:)([^\s;|&]+)`)

	// Slack incoming-webhook URL -- this tool's own primary secret. The path after
	// /services/ IS the credential (T…/B…/token), so mask it while keeping the host
	// so the notification still says where it points. The character class excludes
	// `?` and punctuation, so a trailing query string or sentence period survives.
	//
	// Must stay at least as permissive as what config.ResolveWebhook ACCEPTS, or a
	// webhook the tool happily uses is one it cannot mask. That check compares
	// strings.EqualFold(u.Hostname(), …), so the host is case-insensitive and
	// Hostname() drops any port -- hence (?i) and the optional `:port` here.
	// Without them `https://HOOKS.SLACK.COM/services/T/B/tok` and
	// `https://hooks.slack.com:443/services/T/B/tok` reached Slack in the clear.
	slackWebhookRe = regexp.MustCompile(`(?i)(\bhooks\.slack\.com(?::\d+)?/services/)[A-Za-z0-9/_-]+`)

	// Provider token shapes that are recognisable on their own.
	tokenShapeRes = []*regexp.Regexp{
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}`),   // GitHub
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), // GitHub fine-grained
		regexp.MustCompile(`\bsk-[A-Za-z0-9-]{16,}`),         // OpenAI-style
		regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{16,}`),    // Anthropic
		regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`), // Slack
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),           // AWS access key id
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),      // Google API key
	}

	// A PEM private-key block, masked whole so the other rules cannot nibble at it.
	pemRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

// keepPrefix replaces the match with group 1 followed by the mask, so the readable
// part (key name, flag, scheme, host) survives and only the credential is removed.
func keepPrefix(re *regexp.Regexp, s string) string {
	return re.ReplaceAllString(s, "${1}"+mask)
}

// Auth scheme words that are never themselves a credential. `Authorization:` is
// handled by authHeaderRe, which runs first and leaves `Authorization: Bearer <mask>`
// behind. The generic key/value rule then matches that same span (its key alternation
// includes "auth") with "Bearer" as the value and would mask the scheme too, giving
// the unreadable `Authorization: <mask> <mask>`. Skip those values.
var authSchemeWords = map[string]bool{
	"bearer": true, "basic": true, "token": true, "digest": true,
}

// keepPrefixSkipSchemes is keepPrefix that leaves a match alone when the captured
// value is a bare auth scheme word or is already the mask.
func keepPrefixSkipSchemes(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		v := strings.ToLower(strings.Trim(sub[2], `"'`))
		if v == strings.ToLower(mask) || authSchemeWords[v] {
			return m
		}
		return sub[1] + mask
	})
}

// RedactSecrets masks inline credentials in text bound for Slack.
//
// Order matters: the PEM block goes first so nothing chews on its interior, then the
// Authorization header before the generic key/value rule (otherwise "Authorization"
// matches as a credential-shaped key and only the scheme word is masked, leaving the
// credential after it), then the scoped flag rules, then the standalone token shapes.
//
// Exported so the handler package can redact BEFORE it truncates a field: several
// patterns carry minimum-length floors, and truncation can cut a token below its
// floor so the leftover fragment slips past the redaction in Post. Running twice
// is harmless — a mask never matches as a fresh credential.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = pemRe.ReplaceAllString(s, mask)
	s = keepPrefix(authHeaderRe, s)
	s = keepPrefix(flagSecretRe, s)
	s = keepPrefixSkipSchemes(kvSecretRe, s)
	s = keepPrefix(sshpassRe, s)
	s = keepPrefix(dbPassRe, s)
	s = keepPrefix(redisRe, s)
	s = keepPrefix(userPassRe, s)
	s = urlCredRe.ReplaceAllString(s, "${1}"+mask+"${3}")
	s = keepPrefix(slackWebhookRe, s)
	s = keepPrefix(bearerRe, s)
	for _, re := range tokenShapeRes {
		s = re.ReplaceAllString(s, mask)
	}
	return s
}
