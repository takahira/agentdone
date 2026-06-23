package handler

import "testing"

// TestQuestionContract is the single, contract-keyed table of looksLikeQuestion
// behaviour. Each group is keyed by the clause(s) it covers in
// docs/question-detection.md (F# fire, S# silent, D# deliberate decision). It
// replaces the previously scattered,
// review-round-named question tests (TestLooksLikeQuestion / _Review6 / _Review7
// / *English) so that the intended behaviour and its rationale live in ONE place
// next to the doc — adding a case here without updating docs/question-detection.md
// (or vice-versa) should feel wrong.
//
// Each group's clause string lists EVERY doc clause it covers (a single group
// often exercises both a FIRE row and the Decision that governs it, e.g.
// "D1/F9/S4"); not every clause has its own group. Locale-independence (D7) is a
// separate test below, since it varies AGENTDONE_LANG rather than the input.
func TestQuestionContract(t *testing.T) {
	groups := []struct {
		clause string // matches a row/decision id in docs/question-detection.md
		fire   []string
		silent []string
	}{
		{clause: "F1 last sentence ends in ?/？", fire: []string{
			"これをコミットしてよいですか？",
			"**続行してよいですか？**",
			"「このまま削除してもよいですか？」",
			"コミットしてよいですか (y/n)",
			"Want me to commit this?",
			"このまま進めますかー？",
		}},
		{clause: "F2 English question phrase", fire: []string{
			"Should I update README.md and push the change",
			"Should I bump the version to v1.2.3 and tag it",
			"3 tests failed. Should I fix all of them",
			"I can do that. Should I proceed",
			"I can dig into the flaky test. Want me to do that",
			"Done with the refactor. Should I also update the changelog file",
			"I added the tests. Would you like me to run the full suite now",
			"Want me to commit and push these changes to origin main now",
			"The plan is ready. Is it ok to start implementing",
		}},
		{clause: "F3/D3 Japanese sentence-final interrogative (incl. D3 大丈夫ですか/よろしいですか)", fire: []string{
			"コミットして進めてよいですか",
			"次は A と B のどちらにしますか",
			"このまま進めましょうか",
			"このブランチで進める形でよろしいでしょうか",
			"テストも実行しますか",
			"進めますか、それとも一旦止めますか",
			"命名はこの案でいかがですか",
			// D3: the interrogative forms of the stems removed from questionPhrases.
			// Deleting either from jaFinalPhrases must now fail a test (this positive
			// half of D3 was previously uncovered).
			"この設定を削除して大丈夫ですか",
			"このまま進めてよろしいですか",
		}},
		{clause: "F4 Japanese request form", fire: []string{
			"デプロイ完了です。動作を確認してください",
		}},
		{clause: "F5 question above option rows / table / rule", fire: []string{
			"どちらにしますか？\n1. そのまま進める\n2. 一旦止める",
			"確認事項です。\n1. このままコミットしてよいですか",
			"どちらの案にしますか？\n\n| 案 | 内容 |\n| A | そのまま進める |",
			"このコマンドを実行してよいですか？\n```bash\nrm -rf dist\n```",
			"Should I apply this patch?\n```diff\n- old\n+ new\n```",
			"Should I proceed with the migration?\n\n---",
			"どの言語を追加しますか？\n\n1. Go\n2. Rust\n3. Zig\n4. C\n5. C++\n6. Java\n7. Kotlin",
			"どの言語を追加しますか？\n1.\n2.\n3.\n4.\n5.\n6.\n7.\n8.",
		}},
		{clause: "F6 question + closing pleasantry", fire: []string{
			"Which port should the server use? Thanks.",
			"この方針で進めてよいですか？よろしくお願いします。",
			"Should I delete the stale branches? I'll hold off until you confirm.",
			"Should I merge this PR?\nNote: CI is still running.",
			"Should I ship it? ✅✅✅",
			"**続行してよいですか**\n（備考：CI は後で流します）",
		}},
		{clause: "F7 question + trailing sign-off", fire: []string{
			"Should I refactor this? Let me know.",
		}},
		{clause: "F8 wholly-quoted question / lead-in label + quote", fire: []string{
			"やりました。\n（このまま push してよいですか）",
			"My question: \"Should I deploy now?\"",
			"Quick check — \"Continue with the rebase?\"",
			"確認です：「このまま削除してよいですか？」",
			"My question: «Proceed with the deploy?»",
		}},
		{clause: "F10/D6 prolongation-stretched ja question (word end past ー〜～…)", fire: []string{
			"このまま進めますかー",
		}},

		{clause: "S1 plain completion report", silent: []string{
			"全部できました。",
			"All tests pass and the change is pushed.",
			"2 files changed and everything is pushed.",
			"Finished. Tests pass and the build is green.",
			"All done. I refactored the code and I should investigate the flaky test next.",
		}},
		{clause: "S2 question phrase NOT in the last sentence", silent: []string{
			"ご確認用のスクリプトを作成し、テストを追加して main にプッシュしました。報告は以上です。",
			"確認をお願いしていた件は対応済みで、結果を集計して報告しました。以上です。",
			"修正は3点あります。詳細は以下の通りです。\n1. パーサ修正\n2. テスト追加\n3. ドキュメント更新",
		}},
		{clause: "S3 sign-off offer of more work", silent: []string{
			"All tests pass. Let me know if you want me to dig deeper.",
			"Everything is committed and pushed. Let me know how it goes",
			"I'll go ahead and run the suite, then report back.",
			"All tests pass. Please let me know if you want me to dig deeper.",
			"Done. Just let me know if you want me to continue.",
			"**Let me know if you want me to continue.**",
			"If you want me to also update the docs, just say so.",
			"Happy to refactor further if you want me to take another pass.",
			"The old implementation was, shall we say, fragile - it is now replaced.",
		}},
		{clause: "S5 reported / quoted question (surrounding prose)", silent: []string{
			"確認ダイアログ「削除しますか」は自動でスキップするようにしました。",
			"「続行しますか」というプロンプトには自動で yes を返します。",
			"The installer asked 'Should I overwrite existing files?' and I answered yes automatically.",
			"I suppressed the \"Do you want to continue?\" prompt with the -y flag.",
			"- Added handling for the 'Do you want to continue' prompt",
		}},
		{clause: "S8 ja reason/musing forms containing ますか", silent: []string{
			"テストは後で実行しますから、安心してください。",
			"これで動きますかね、たぶん大丈夫です。",
			"どちらにしても修正は完了しています。",
			"「進めますかー」と言われたので、そのまま実行しました。",
			"進めますかーと聞かれた気がしますが、完了しています。",
		}},
		{clause: "S9/D3 declarative use of して大丈夫 / 進めてよろしい (NOT the interrogative)", silent: []string{
			"検証の結果、この設定は削除して大丈夫です。",
			"進めてよろしいとの承認をいただいたので、デプロイまで完了しました。",
		}},

		{clause: "D1/F9/S4 'or' is not a choice marker (which/whether are)",
			fire: []string{
				"Let me know which approach you want me to take.",
				"Let me know whether you want me to continue.",
			},
			silent: []string{
				"Let me know if you want me to add tests or update the docs later.",
				"Done. Let me know if you want me to write unit or integration tests.",
				"All set. Let me know if you want me to deploy now or wait for review.",
				"Let me know if you want me to proceed with option B or stick with A.",
			}},
		{clause: "D2/S6 reporting-verb lead-in is reported speech, not rescued", silent: []string{
			"The installer asked: \"Should I overwrite existing files?\"",
			"The user said: \"Should I proceed?\"",
			"The CLI prompted — \"Continue with the rebase?\"",
			"The dialog printed: \"Continue?\"",                  // reportingVerb "printed"
			"The dialog said “Should I continue?” and moved on.", // curly “” + reportingVerb "said"
			"The dialog «Should I continue?» was auto-dismissed.",
			"Die Meldung „Should I proceed?“ habe ich automatisch bestätigt.",
		}},
		{clause: "D4/D5/S7 fences stripped only when properly paired (no ends-in-? fast path)",
			fire: []string{
				// unterminated / mismatched fence must NOT swallow a trailing question
				"途中経過です。\n```\nx := 1\n~~~\nこのまま進めてよいですか？",
			},
			silent: []string{
				"リファクタ完了です。最終形は以下です。\n```swift\nvar name: String?\n```",
				"クエリを修正しました。\n```sql\nWHERE deleted = ?\n```",
				// a ~~~ inside a ``` block is content, not a delimiter; "```go" doesn't close
				"リファクタ完了です。\n```go\nx := 1\n~~~\nreturn parse(input)?\n```",
			}},
	}

	for _, g := range groups {
		for _, s := range g.fire {
			if !looksLikeQuestion(s) {
				t.Errorf("[%s] looksLikeQuestion(%q) = false, want true (FIRE)", g.clause, s)
			}
		}
		for _, s := range g.silent {
			if looksLikeQuestion(s) {
				t.Errorf("[%s] looksLikeQuestion(%q) = true, want false (SILENT)", g.clause, s)
			}
		}
	}
}

// TestQuestionContract_D7LocaleIndependent locks in Decision D7: detection
// checks BOTH languages regardless of AGENTDONE_LANG (the locale only picks
// notification labels). An English question must fire under ja and a Japanese
// question under en. (TestMain sets ja for the binary; this varies it per case.)
func TestQuestionContract_D7LocaleIndependent(t *testing.T) {
	const enQ = "Should I deploy now"
	const jaQ = "デプロイしてよいですか"
	for _, lang := range []string{"en", "ja"} {
		t.Setenv("AGENTDONE_LANG", lang)
		if !looksLikeQuestion(enQ) {
			t.Errorf("AGENTDONE_LANG=%s: en question %q did not fire", lang, enQ)
		}
		if !looksLikeQuestion(jaQ) {
			t.Errorf("AGENTDONE_LANG=%s: ja question %q did not fire", lang, jaQ)
		}
	}
}
