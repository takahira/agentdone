package handler

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// looksLikeQuestion reports whether text ends like a confirmation question, in
// which case it is surfaced regardless of the time threshold (a plain-text
// confirmation arrives on Stop, not Notification).
//
// The match is scoped to the LAST PROSE SENTENCE: a real confirmation is at the
// end, whereas a completion summary that merely mentions e.g. "ご確認" earlier
// must not be misread as one. English (SVO) front-loads the phrase ("Should I …
// <long object clause>"), so a fixed-width tail window would miss it — the last
// sentence catches it regardless of word order. Fenced code blocks, option rows,
// markdown tables, horizontal rules and closing pleasantries that trail a
// question are skipped so the question they follow is still seen. Both languages'
// phrases are checked: the display locale only picks the labels, but a session
// run in one language routinely ends on a question in the other.
//
// There is deliberately NO whole-text "ends in ?" fast path: trimDecor strips a
// trailing ``` fence, which would expose a final code line ending in '?'
// (`var name: String?`, `parse(input)?`) and misread a pure completion as a
// question. Routing everything through lastProseSentence drops the code block first.
func looksLikeQuestion(text string) bool {
	return questionSignal(lastProseSentence(text))
}

// questionSignal reports whether s itself reads as a confirmation question:
// it ends in a question mark (once decoration is stripped) or contains a
// question phrase. Used both on the final candidate sentence and inside
// skippable(), so a question that is itself parenthesised or sits on a list
// row — "（続けてよいですか）", "2. テストも実行しますか" — is never skipped
// over as if it were a trailing aside.
func questionSignal(s string) bool {
	// A question QUOTED inside a report — "I auto-answered the 'Continue?' prompt",
	// 確認ダイアログ「削除しますか」は自動でスキップ — is reported speech, not the
	// assistant asking. Drop quoted spans before matching, UNLESS what remains is
	// just the quote itself (nothing alphanumeric outside it) or a mere lead-in
	// label introducing it (`My question: "…?"`, `Quick check — "…?"`) — there the
	// quote IS the message and must still be read.
	if q := stripQuoted(s); hasAlnum(q) && !isQuoteLeadIn(q) {
		s = q
	}
	d := trimDecor(s)
	if strings.HasSuffix(d, "?") || strings.HasSuffix(d, "？") {
		return true
	}
	low := strings.ToLower(d)
	// A completion sign-off / offer ("Let me know if you want me to dig deeper.",
	// "Happy to take another pass.") reads like a question but waits on nothing —
	// unless it presents an explicit choice, which makes it a real decision point.
	if isOffer(low) {
		return false
	}
	tail := low + " "
	for _, p := range questionPhrases {
		if strings.Contains(tail, p) {
			return true
		}
	}
	return jaFinalSignal(d)
}

// stripQuoted blanks the content of quoted spans (「」『』 “” ‘’, straight "…",
// and boundary-delimited '…') so a question quoted inside a report is not read
// as the assistant asking. Straight single quotes are only treated as a pair
// when both ends sit on a word boundary, so contractions (I'll, don't) are left
// intact.
func stripQuoted(s string) string {
	s = quotedSpanRe.ReplaceAllString(s, " ")
	return sqQuotedRe.ReplaceAllString(s, "$1 $2")
}

// isQuoteLeadIn reports whether s (the remainder after quoted spans were
// blanked) is just a label introducing the quote — "My question:", "Quick
// check —" — rather than surrounding prose. In that case the quoted question IS
// the message and stripQuoted must not be applied, so the question still fires.
// A reported-speech sentence ("The installer asked … and I answered yes.") does
// not end on a lead-in punctuation, so it is still stripped.
func isQuoteLeadIn(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false // handled by the hasAlnum check, not here
	}
	switch r, _ := utf8.DecodeLastRuneInString(t); r {
	case ':', '：', '—', '–', '-', '→':
	default:
		return false
	}
	// A reporting verb makes this a report of someone else's question
	// ("The installer asked: …", "ダイアログが表示した：…"), NOT the assistant
	// introducing its own — so it is reported speech and the quote must still be
	// stripped. Only a verb-less label ("My question:", "Quick check —") is a
	// genuine lead-in that keeps the quoted question.
	low := strings.ToLower(t)
	for _, v := range reportingVerbs {
		if strings.Contains(low, v) {
			return false
		}
	}
	return true
}

// quotedSpanRe matches a balanced quoted span. Corner brackets 「」『』, curly
// “” ‘’, guillemets «», German low-high „…“, and straight "…". An UNbalanced
// quote (a lone opener) is intentionally not matched — there is no reliable way
// to bound it without eating real text. The German „ closes ONLY on the curly “
// (not a straight "), so a lone „ can't greedily swallow a later straight-quoted
// span in the same sentence.
var quotedSpanRe = regexp.MustCompile("「[^」]*」|『[^』]*』|“[^”]*”|‘[^’]*’|«[^»]*»|„[^“]*“|\"[^\"]*\"")

var sqQuotedRe = regexp.MustCompile(`(^|[\s(\[（「『])'[^']*'($|[\s).,!?;:\]）」』])`)

// hasAlnum reports whether s contains any letter or digit (anything that could
// carry meaning), used to tell a wholly-quoted sentence from a quote embedded in
// surrounding prose.
func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// isOffer reports whether s reads as a completion sign-off / offer rather than a
// question: a "let me know …" sign-off (after stripping leading markdown and a
// leading please/just) or an offer phrase ("just say so", "happy to …", "if you
// want me to …"). A sentence that presents an explicit choice via an
// interrogative determiner (which/whether/どちら/それとも — NOT a bare "or", see
// docs/question-detection.md D1) is NOT an offer; it is a real decision.
func isOffer(low string) bool {
	if hasChoiceMarker(low) {
		return false
	}
	g := strings.TrimLeft(low, " *_~`\"'")
	g = strings.TrimPrefix(g, "please ")
	g = strings.TrimPrefix(g, "just ")
	for _, p := range signoffPrefixes {
		if strings.HasPrefix(g, p) {
			return true
		}
	}
	for _, p := range offerPhrases {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

func hasChoiceMarker(low string) bool {
	for _, m := range choiceMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// isClosing reports whether s is a trailing pleasantry / aside ("Thanks.",
// "Note: CI is still running.", "よろしくお願いします。") that often follows a
// question but is not itself one, so the scan steps over it to find the question.
func isClosing(low string) bool {
	for _, p := range closingPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return strings.Contains(low, "よろしくお願い") || strings.Contains(low, "失礼し")
}

// jaFinalSignal reports whether s carries a Japanese sentence-final
// interrogative form at a word end: the rune after the match must not be a
// letter. That keeps "〜ますか。", "〜ますか (y/n)" and "〜ますか、それとも…"
// firing while "〜しますから" (reason) and "〜ますかね" (musing) — which merely
// contain the same runes — pass silently.
func jaFinalSignal(s string) bool {
	for _, p := range jaFinalPhrases {
		for idx := 0; ; {
			j := strings.Index(s[idx:], p)
			if j < 0 {
				break
			}
			end := idx + j + len(p)
			// Skip any prolongation/elongation run (ー〜～…): it is a colloquial
			// stretch of the question ("進めますかー") and a letter by Unicode (ー is
			// category Lm), but does not break the word. Then require a TRUE word end
			// — end-of-string or a non-letter. This fires "ますかー" / "ますかー？" but
			// keeps "ますかーと言った" (a quoted/reported "ますかー" mid-sentence),
			// "〜しますから" and "〜ますかね" silent.
			k := end
			for k < len(s) {
				r, sz := utf8.DecodeRuneInString(s[k:])
				if !isProlong(r) {
					break
				}
				k += sz
			}
			if k == len(s) {
				return true
			}
			if r, _ := utf8.DecodeRuneInString(s[k:]); !unicode.IsLetter(r) {
				return true
			}
			idx = end
		}
	}
	return false
}

// isProlong reports whether r is a Japanese prolongation / elongation mark. They
// classify as letters in Unicode but only stretch the preceding sound, so a
// question form followed by one is still at a word end.
func isProlong(r rune) bool {
	return r == 'ー' || r == '〜' || r == '～' || r == '…'
}

// trimDecor strips trailing whitespace and markdown/quote decoration so
// "**…?**", 「…ですか？」 or «…?» still reads as ending in a question mark. The
// closing guillemet/curly marks (»”’“) match those added to quotedSpanRe, so a
// wholly-quoted question fires whatever quote style wraps it.
func trimDecor(s string) string {
	return strings.TrimRight(s, " \t\r\n　*_~`\"'」』）)】>＞»”’“")
}

// listItemRe matches an option/list row ("- run it", "2. いいえ", "a) yes") and
// the bare ordinal fragment ("2.") sentence-splitting leaves behind. The digit
// and letter forms require their punctuation (and "-"/"*" their space), so
// digit-leading prose ("3 tests failed. Should I fix them") and markdown
// emphasis ("**注意**") are NOT mistaken for list rows; "・はい"-style Japanese
// bullets carry no space and are matched bare.
var listItemRe = regexp.MustCompile(`^([•・]|[-*] |[0-9]{1,3}[.)、．]([ \t　]|$)|[a-dA-D][.)]([ \t　]|$))`)

// skippable reports text that cannot itself be the confirmation question — an
// option row, a bare "(y/n)", a sign-off ("Let me know."), or a fragment too
// short to be prose — so the scan keeps looking at what precedes it
// ("Proceed? (y/n)", numbered choice lists, "Should I …? Let me know.").
// Anything carrying a question signal of its own is never skippable, whatever
// its shape: the question may itself be parenthesised or numbered.
func skippable(s string) bool {
	if questionSignal(s) {
		return false
	}
	if len([]rune(s)) <= 2 {
		return true
	}
	t := strings.TrimSpace(s)
	// Pure punctuation / symbols: a horizontal rule ("---"), an emoji tail
	// ("✅✅✅"), an ellipsis. Nothing here can be the question.
	if !hasAlnum(t) {
		return true
	}
	low := strings.ToLower(trimDecor(s))
	if isOffer(low) || isClosing(low) {
		return true
	}
	if (strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")")) ||
		(strings.HasPrefix(s, "（") && strings.HasSuffix(s, "）")) {
		return true
	}
	// A markdown table row ("| A | proceed |") that trails a question.
	if strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|") {
		return true
	}
	return listItemRe.MatchString(s)
}

// maxSkipLines bounds how many non-empty trailing rows lastProseSentence will
// step over before giving up — generous, because Claude routinely lists many
// options/files (a blank line is free, so it never eats the budget) and the
// question sits above them.
const maxSkipLines = 50

// lastProseSentence returns the sentence the confirmation question would live
// in: the last prose sentence, after dropping fenced code blocks and skipping
// (bounded) over option rows, table rows, rules and asides that often trail one.
// Blank lines are skipped for free; only non-empty skipped rows count toward the
// budget, so a question above seven-plus option rows is still found.
func lastProseSentence(t string) string {
	lines := stripFencedCode(strings.Split(t, "\n"))
	skipped := 0
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if line == "" {
			continue
		}
		if !skippable(line) {
			sents := splitSentences(line)
			for j := len(sents) - 1; j >= 0; j-- {
				if s := strings.TrimSpace(sents[j]); s != "" && !skippable(s) {
					return s
				}
			}
		}
		if skipped++; skipped > maxSkipLines {
			return ""
		}
	}
	return ""
}

// stripFencedCode drops PROPERLY-PAIRED fenced code blocks (``` or ~~~) and the
// fence lines, so code — which is not a question, and whose last line may end in
// '?' (`var name: String?`) — never reaches question detection.
//
// Two passes, so only a block with a real matching close is removed:
//   - the close must be the SAME marker char as the open and at least as long,
//     and carry no info string — a ~~~ inside a ``` block (or "```go") is content;
//   - an UNTERMINATED opener is left in place rather than swallowing the rest of
//     the message, so a trailing confirmation question after a malformed/mismatched
//     fence is still seen (missing a waiting question is the worst failure here).
func stripFencedCode(lines []string) []string {
	drop := make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		open := fenceMarker(strings.TrimSpace(lines[i]))
		if open == "" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if fenceCloses(strings.TrimSpace(lines[j]), open) {
				for k := i; k <= j; k++ {
					drop[k] = true
				}
				i = j
				break
			}
		}
		// no matching close found: leave lines[i] (and the rest) intact
	}
	out := lines[:0:0]
	for i, ln := range lines {
		if !drop[i] {
			out = append(out, ln)
		}
	}
	return out
}

// fenceMarker returns the opening fence run (3+ of ` or ~) at the start of a
// trimmed line — info string allowed — or "" if the line does not open a fence.
func fenceMarker(t string) string {
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n]
		}
	}
	return ""
}

// fenceCloses reports whether a trimmed line closes a fence opened with open: the
// SAME char, at least as long, and nothing but the fence chars (a close carries
// no info string, so "```go" closes nothing).
func fenceCloses(t, open string) bool {
	if len(t) < len(open) || t[0] != open[0] {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != open[0] {
			return false
		}
	}
	return true
}

// splitSentences splits s into sentences (terminators kept). Japanese
// terminators (。．！？) always end a sentence; ASCII .!? end one only when
// followed by whitespace or end-of-text, so "v1.2.3", "README.md" or a URL
// does not cut the sentence the question lives in.
func splitSentences(s string) []string {
	var out []string
	rs := []rune(s)
	start := 0
	for i, r := range rs {
		boundary := false
		switch r {
		case '。', '．', '！', '？':
			boundary = true
		case '.', '!', '?':
			boundary = i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\t'
		}
		if boundary {
			out = append(out, string(rs[start:i+1]))
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}
