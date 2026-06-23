package handler

import (
	"fmt"
	"os"
	"strings"
)

// messages holds every user-facing label in one language. Notifications default
// to English; set AGENTDONE_LANG=ja (or any of LC_ALL / LC_MESSAGES / LANG
// starting with "ja") for Japanese.
type messages struct {
	lang string

	// headers (each already includes its emoji)
	done          string
	waiting       string // a plain-text confirmation question on Stop
	waitingChoice string // PreToolUse header; {subject} is replaced with planApproval/choiceQ
	permission    string
	idle          string
	attention     string
	errorEnd      string

	// PreToolUse subjects
	planApproval string
	choiceQ      string

	// field labels
	session  string
	prompt   string
	location string
	model    string
	output   string
	skill    string
	did      string
	confirm  string
	asked    string
	content  string
	errLabel string

	sep     string // label/value separator
	metaSep string // separator between the model/output/skill meta items
}

// questionPhrases are lowercased substrings that, in the last prose sentence of
// an assistant message, mark it as a confirmation question (used when the text
// does not already end in "?"). Detection always checks BOTH languages — the
// display locale only picks the notification labels, and a session run in one
// language routinely ends on a question in the other; matching only the active
// catalog left such turns completely silent.
//
// Trailing spaces make the English phrases match on a word boundary
// (looksLikeQuestion pads the haystack with a space), so "should i " fires on
// "Should I commit" but not "I should investigate". "is it ok" (no trailing
// space) also covers "okay". The Japanese entries here are REQUEST forms that
// appear mid-sentence (ご確認ください / 確認をお願いします); the sentence-final
// interrogative forms live in jaFinalPhrases, which need a word boundary.
var questionPhrases = []string{
	// English
	"should i ", "shall i ", "shall we ", "do you want ", "would you like ",
	"want me to ", "ok to ", "okay to ", "is it ok",
	// Japanese request forms
	"ご確認", "確認をお願い", "確認ください", "確認してください",
}

// jaFinalPhrases are Japanese sentence-FINAL interrogative forms. They are
// matched only at a word end (the rune after the match must not be a letter —
// see jaFinalSignal): a plain substring match fired on "〜しますから" (reason)
// and "〜ますかね" (musing), which contain "ますか" without asking anything.
// "どちらにし" was dropped from the old list for the same reason — "どちらにし
// ても" is a conjunction; the real questions (どちらにしますか / どちらにしま
// しょうか) end in forms below.
// "大丈夫ですか" / "いかがですか" are sentence-final interrogatives; the plain
// stems "して大丈夫" / "進めてよろしい" were dropped from questionPhrases because a
// substring match fired on the declarative "削除して大丈夫です" (a safety REPORT)
// and the quoted "進めてよろしいとの承認をいただいた". The interrogative forms
// "…よろしいですか" / "…大丈夫ですか" are caught here at a word end instead.
var jaFinalPhrases = []string{
	"いいですか", "よいですか", "良いですか", "よろしいですか", "ましょうか",
	"どうしますか", "でよいですか", "でしょうか", "大丈夫ですか", "いかがですか", "ますか",
}

// signoffPrefixes mark sentences that READ like questions but are completion
// sign-offs: "Let me know if you want me to dig deeper." offers more work, it
// does not wait on an answer — without the guard it would match "want me to "
// above and bypass the time threshold. Compared (lowercased) against the start
// of the sentence. A sign-off is no question, but it is skippable: in
// "Should I refactor this? Let me know." the scan steps over it and still
// finds the real question before it.
var signoffPrefixes = []string{"let me know"}

// offerPhrases mark a sentence as a completion OFFER rather than a question:
// "just say so" / "if you want me to …" / "happy to …" offer optional follow-up
// work and wait on nothing. Without this they match questionPhrases ("want me
// to ") and bypass the time threshold. "shall we say" is the idiom, not a
// "shall we …?" question. Checked anywhere in the (lowercased) sentence.
var offerPhrases = []string{"just say so", "if you want me to", "happy to ", "feel free", "shall we say"}

// choiceMarkers flip an offer back into a real question: a sentence that
// presents an explicit choice ("let me know WHICH approach …", "どちらにしますか")
// is a decision the user must make, not an optional offer.
//
// " or " is deliberately NOT a marker: a completion sign-off routinely lists
// optional follow-ups with a benign "or" ("let me know if you want me to add
// tests or update the docs"), and treating that as a question made every such
// sign-off bypass the threshold and fire a spurious ping (a known
// regression). The interrogative determiners which/whether DO mark a real
// question and stay. "which " / "whether " carry a trailing space so they don't
// fire inside "switch"/"weather".
var choiceMarkers = []string{"which ", "whether ", "どちら", "それとも"}

// reportingVerbs identify a lead-in that REPORTS someone else's question rather
// than introduces the assistant's own ("The installer asked: …", "ダイアログが
// 表示した：…"). When one appears before a quoted question, the quote is reported
// speech and stays stripped (see isQuoteLeadIn). Checked as lowercased
// substrings of the short lead-in fragment.
var reportingVerbs = []string{
	"asked", "answered", "said", "says", "printed", "prints", "prompted",
	"prompts", "queried", "replied", "reported", "mentioned", "told", "warned",
	"showed", "shows", "displayed", "responded",
	"聞か", "言わ", "尋ね", "表示", "問わ", "問い",
}

// closingPrefixes mark a trailing pleasantry / aside that follows a question but
// is not one ("Thanks.", "Note: CI is still running.", "FYI …"), so the scan
// steps over it to the question above. Matched against the start of the
// (lowercased, decoration-trimmed) sentence.
var closingPrefixes = []string{
	"thanks", "thank you", "note:", "fyi",
	"i'll hold off", "i'll wait", "i will hold off", "i will wait",
}

// line renders "label<sep>value".
func (m messages) line(label, value string) string { return label + m.sep + value }

// duration renders the elapsed time, or "" when it is unknown (no start).
func (m messages) duration(elapsed int64) string {
	if elapsed <= 0 {
		return ""
	}
	if m.lang == "ja" {
		return fmt.Sprintf("（%d分%d秒）", elapsed/60, elapsed%60)
	}
	return fmt.Sprintf(" (%dm %ds)", elapsed/60, elapsed%60)
}

var enMessages = messages{
	lang:          "en",
	done:          ":white_check_mark: Done",
	waiting:       ":raised_hand: Waiting for confirmation",
	waitingChoice: ":raised_hand: Waiting for confirmation ({subject})",
	permission:    ":raised_hand: Waiting for permission",
	idle:          ":raised_hand: Waiting for input",
	attention:     ":raised_hand: Waiting for you",
	errorEnd:      ":x: Ended on error",
	planApproval:  "plan approval",
	choiceQ:       "a multiple-choice question",
	session:       "Session",
	prompt:        "Prompt",
	location:      "Where",
	model:         "Model",
	output:        "Output",
	skill:         "Skill",
	did:           "Did",
	confirm:       "Asking",
	asked:         "Question",
	content:       "Detail",
	errLabel:      "Error",
	sep:           ": ",
	metaSep:       " · ",
}

var jaMessages = messages{
	lang:          "ja",
	done:          ":white_check_mark: 完了",
	waiting:       ":raised_hand: 確認待ち",
	waitingChoice: ":raised_hand: 確認待ち（{subject}）",
	permission:    ":raised_hand: 権限承認待ち",
	idle:          ":raised_hand: 入力待ち",
	attention:     ":raised_hand: 確認/入力待ち",
	errorEnd:      ":x: エラー終了",
	planApproval:  "プラン承認",
	choiceQ:       "選択式の質問",
	session:       "セッション名",
	prompt:        "プロンプト",
	location:      "場所",
	model:         "モデル",
	output:        "出力",
	skill:         "スキル",
	did:           "やったこと",
	confirm:       "確認内容",
	asked:         "聞かれたこと",
	content:       "内容",
	errLabel:      "エラー",
	sep:           "：",
	metaSep:       "　",
}

// activeMessages returns the catalog for the active language. It reads the
// environment each call (hooks are short-lived, so this is negligible) which
// keeps it test-friendly.
func activeMessages() messages {
	if wantJapanese() {
		return jaMessages
	}
	return enMessages
}

func wantJapanese() bool {
	if v := os.Getenv("AGENTDONE_LANG"); v != "" {
		return strings.HasPrefix(strings.ToLower(v), "ja")
	}
	// POSIX locale precedence: LC_ALL overrides LC_MESSAGES overrides LANG.
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return strings.HasPrefix(strings.ToLower(v), "ja")
		}
	}
	return false
}
