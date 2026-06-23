package handler

import (
	"strings"
	"testing"

	"github.com/takahira/agentdone/internal/state"
	"github.com/takahira/agentdone/internal/transcript"
	"github.com/takahira/agentdone/pkg/cchooks"
)

func TestBuildStopText(t *testing.T) {
	turn := state.Turn{SessionTitle: "Review changes", Prompt: "テストを実行して"}
	sum := transcript.Summary{OutputTokens: 27300, Model: "opus-4-8", Skill: "code-review"}
	in := &cchooks.Stop{LastAssistantMessage: "並列sweepを集計して報告しました。"} // empty Cwd -> no 場所 line / git call

	got := buildStopText(":white_check_mark: 完了", false, 600, turn, in, sum)
	for _, want := range []string{
		":white_check_mark: 完了（10分0秒）",
		"セッション名：Review changes",
		"プロンプト：テストを実行して",
		"モデル：opus-4-8",
		"出力：27.3k tok",
		"スキル：code-review",
		"やったこと：並列sweepを集計して報告しました。",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// A confirmation question relabels the summary line.
	ask := buildStopText(":raised_hand: 確認待ち", true, 30, turn, in, transcript.Summary{})
	if !strings.Contains(ask, "確認内容：") {
		t.Errorf("expected 確認内容 label, got:\n%s", ask)
	}
}

func TestBuildStopTextNoDuration(t *testing.T) {
	// elapsed <= 0 (no recorded start) omits the （…） duration parenthetical.
	got := buildStopText(":raised_hand: 確認待ち", true, 0,
		state.Turn{Prompt: "確認です"}, &cchooks.Stop{}, transcript.Summary{})
	if strings.Contains(got, "（") {
		t.Errorf("expected no duration parenthetical, got:\n%s", got)
	}
	if !strings.HasPrefix(got, ":raised_hand: 確認待ち") {
		t.Errorf("unexpected header:\n%s", got)
	}
}
