# agentdone

[English](README.md) | **日本語**

**エージェントが*本当に*終わったときだけ鳴る、Claude Code 向け Slack 通知。**

`jq` / Node / Python 不要の単一静的バイナリ。Claude Code にフックして、低ノイズで
文脈の濃い通知を送ります。肝は、**バックグラウンド作業が走っている間は早すぎる
「完了」通知を抑止する**こと。

[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.24+](https://img.shields.io/badge/go-1.24%2B-blue.svg)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey.svg)](https://github.com/takahira/agentdone)

> このページは要点のみです。**設定・トラブルシュート・制約・仕組みの詳細と、
> 常に最新の正本は[英語版 README](README.md)**を参照してください。

---

## 何を解決するか

並列処理や Workflow、長い `&` ジョブを走らせたままターンが終わると、Claude Code は
「バックグラウンドコマンド完了」メッセージを差し込みます。素朴な通知フックはこれで
**まだ作業中なのに「✅ 完了」を誤発火**し、Slack と注意力を汚します。

agentdone は Claude Code が `Stop` フックに載せる公式の `background_tasks` を読み、
何か実行中の間は黙り、作業が**本当に**終わったときに**1 通だけ**正しい通知を送ります。

---

## クイックスタート

```sh
curl -fsSL https://raw.githubusercontent.com/takahira/agentdone/main/install.sh | sh
agentdone init   # フックを配線し、Slack Webhook を一度だけ尋ねます
```

Go ツールチェインがあればソースから:

```sh
go install github.com/takahira/agentdone/cmd/agentdone@latest
agentdone init
```

以上です。次にターンが本当に終わったとき、きれいな通知が 1 通届きます。設定ファイルは
不要。環境変数で微調整できます。

## 主な通知

| とき | 通知 |
| --- | --- |
| ターン完了（既定 ≥ 300 秒、または平文の確認質問） | `✅ Done` / `✋ Waiting for confirmation`（セッション名・プロンプト・repo·branch・モデル・出力トークン・skill・要約） |
| API エラーで終了（レート制限・過負荷・認証 …） | `❌ Ended on error`（所要時間に関係なく必ず送信） |
| ターン終了時に背景作業が実行中 | *（何も送らない＝抑止）* |
| 許可 / アイドルのプロンプト | `✋ Waiting for permission` / `✋ Waiting for input`（**ターミナルのみ**） |
| `AskUserQuestion` / `ExitPlanMode` | `✋ Waiting for confirmation`（質問 / プラン抜粋つき） |

## 日本語通知

通知文は既定で英語、**`AGENTDONE_LANG=ja` で日本語**になります（`AGENTDONE_LANG` が最優先。
未設定時は POSIX ロケール `LC_ALL` > `LC_MESSAGES` > `LANG` を参照）。

```sh
# 例: ~/.claude/settings.json の env で固定
{ "env": { "AGENTDONE_LANG": "ja" } }
```

---

詳細（全環境変数・リリース検証・VS Code 拡張での挙動差・既知の制約・仕組み）は
[英語版 README](README.md) にまとまっています。

## ライセンス

MIT
