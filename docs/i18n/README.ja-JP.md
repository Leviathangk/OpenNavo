<div align="center">

<img src="../design/logo/svg/app-icon.svg" width="112" alt="OpenNavo">

# OpenNavo

[English](../../README.md) · [简体中文](README.zh-CN.md) · **日本語** · [Español](README.es-ES.md) · [Português (Brasil)](README.pt-BR.md) · [Русский](README.ru-RU.md) · [Deutsch](README.de-DE.md) · [Français](README.fr-FR.md) · [Italiano](README.it-IT.md) · [繁體中文](README.zh-Hant.md)

**Mac のための Homebrew アプリストア**

7,700 以上の Homebrew Cask の紹介とバージョン履歴を閲覧し、<br>
macOS クライアントでワンクリックでインストール・更新。すべての処理を Homebrew が担い、その進み具合を確認できます。

[![ライセンス](https://img.shields.io/badge/license-GPL--3.0--only-FF7356)](../../LICENSE)
![macOS 13+](https://img.shields.io/badge/macOS-13%2B-111111?logo=apple)
![Tauri 2](https://img.shields.io/badge/Tauri-2-24C8DB?logo=tauri&logoColor=white)
![Nuxt 4](https://img.shields.io/badge/Nuxt-4-00DC82?logo=nuxt&logoColor=white)
![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)

[公式サイト](https://opennavo.com/ja) · [App を探す](https://opennavo.com/ja/discover) · [クライアントをダウンロード](https://opennavo.com/ja/download) · [開発への参加](#開発への参加)

<img src="../../.github/assets/ja-JP/screenshot-home.webp" alt="OpenNavo の日本語ホームページ" width="100%">

</div>

## はじめに

<!-- brand-definition -->
OpenNavo は、Mac 上の Homebrew アプリをワンクリックでインストール・更新できる、無料でオープンソースの Mac 向け Homebrew グラフィカルクライアントであり、そのウェブサイトはバージョン履歴、システム要件、アンインストール方法などの情報を提供する Mac アプリのディレクトリです。
<!-- /brand-definition -->

Homebrew は Mac にソフトウェアをインストールする信頼性の高い方法ですが、コマンドラインは誰にでも使いやすいわけではなく、`brew outdated` では更新内容までは分かりません。OpenNavo は Homebrew Cask にアプリストアのインターフェースを提供します。

- **Web 版**：App の閲覧・検索・比較、各バージョンの更新内容の確認、インストールコマンドのコピーができます。
- **macOS クライアント**：Web 版のすべての機能に加え、自分の Mac の Homebrew でインストール・更新を実行し、入手元を問わず App を丸ごとアンインストールし、キャッシュや残りファイルを慎重に片付け、メニューバーで Mac の状態を監視し、操作を記録します。
- **多言語対応**：ウェブサイトは English、简体中文、繁體中文、日本語、Español、Português (Brasil)、Русский、Deutsch、Français、Italiano の 10 言語に対応しています。macOS クライアントも同じ 10 言語に対応し、管理画面はそのうち English、简体中文、日本語、Español、Português (Brasil)、Русский の 6 言語に対応しています。App の紹介と更新内容は自動翻訳されます。いつでも原文に切り替えられます。
- **アカウント不要**：登録もログインも必要ありません。開くだけで使えます。

[opennavo.com](https://opennavo.com/ja) は、このリポジトリのコードで動いています。

## 機能

### Web 版

- **見つける**：カテゴリ、実際のインストール数に基づくランキング、編集部のおすすめ、コレクション。
- **検索**：英語名・中国語名・ピンイン・別名に対応しています。例えば `weixin` で WeChat、`vscode` で Visual Studio Code が見つかります。<kbd>⌘</kbd> <kbd>K</kbd> でいつでも開けます。
- **App の詳細**：紹介、スクリーンショット、インストール数と更新頻度、依存関係と競合、インストール先、アンインストール時の処理。
- **バージョン履歴**：各バージョンの更新内容をタイムラインで表示し、Homebrew への登録日時も確認できます。翻訳と原文を切り替えられます。
- **Brewfile**：閲覧中に App をリストに追加し、ワンクリックで Brewfile を生成できます。コレクションも直接エクスポートできます。
- **クライアントで開く**：`opennavo://` ディープリンクでインストールを macOS クライアントに引き継ぎます。

### macOS クライアント

- **ワンクリックでインストール・更新・アンインストール**：タスクを順番に実行し、進捗と実際の brew コマンドを表示します。失敗時はログを確認して再試行できます。
- **入手元を問わずアンインストール**：Homebrew、App Store、ディスクイメージ、.pkg インストーラから入れた App を、サイズと最終使用日とともに一覧表示します。削除の前に、見つかった関連ファイル（App のデータ、キャッシュ、環境設定、バックグラウンド項目、インストール記録など）をすべて確認できます。既定ではゴミ箱に移すので、元に戻せます。
- **Mac のクリーンアップ**：読み取り専用のスキャンで、App のキャッシュ、ログ、ブラウザと開発ツールのキャッシュ、Homebrew のダウンロードキャッシュと旧バージョン、削除済みの App が残したファイルを見つけます。古い一時ファイル、不要になったインストーラ、1 か月触っていないプロジェクトのビルド成果物、ゴミ箱、デバイスのバックアップと Xcode アーカイブ、Docker が再利用できる領域も見つけます。結果はリスクごとに分かれ、再生成できるファイルはすぐに削除し、残りファイルとインストーラはゴミ箱に移すので元に戻せます。個人ファイルが勝手に選択されることはありません。実行中の App のキャッシュはスキップします。
- **システムモニタ**：CPU、GPU、メモリ、ディスク、ネットワーク、バッテリー、温度、ファンを一覧でき、直近 1 分または 1 時間のグラフ、CPU と GPU の消費電力、ディスクの状態（SMART）、CPU・メモリ・ディスク・ネットワークを多く使っているプロセスも表示します。各指標はそれぞれ独立したメニューバー項目にでき、数に制限はありません（初期状態では CPU とメモリがオン）。表示はテキストか小さなグラフを選べ、項目をクリックするとその下に詳細が表示されます。通知をオンにすると、注意が必要な問題が起きたときに知らせます。ファンの回転数はいつでも確認でき、手動調整に必要なヘルパーはアプリ内でもメニューバーからでもワンクリックでインストールできます。読み取った値は Mac のメモリ上だけにあり、アップロードもディスクへの保存もしません。
- **更新管理**：定期的な更新確認と一括更新に対応し、自動更新する App（`auto_updates`）や固定された App（pinned）も識別します。起動中の App は同意を得てから更新します。
- **ローカルの操作履歴**：インストール・更新・アンインストールをすべて記録し、エクスポートできます。
- **簡単な初期設定**：Homebrew の環境を検出し、必要ならインストールを案内します。中国国内のミラー（清華 TUNA、中国科学技術大学、Alibaba Cloud）にワンクリックで切り替えられます。
- **その他**：メニューバー常駐、Brewfile のインポートとエクスポート、オフラインのカタログ、クライアント自身の更新。

### 管理画面とコンテンツ管理

- カテゴリ、ウェブサイトの全対応言語のテキスト、アイコンとアクセントカラー、スクリーンショット、おすすめ、コレクション、用語集、クライアントのお知らせを管理できます。
- 更新内容と翻訳、同期タスク、検索分析、フィードバック、クライアントのリリース、ミラー、リモート設定を管理できます。管理者・ロール・権限・監査ログに対応し、変更は復元できます。削除した項目はまずごみ箱に移動します。
- [MCP](https://modelcontextprotocol.io) サービスを内蔵しています。AI Agent は付与された権限の範囲内で App のコンテンツを管理でき、すべての書き込み操作に dry run が用意されています。

## スクリーンショット

<table>
  <tr>
    <td width="50%"><img src="../../.github/assets/ja-JP/screenshot-discover.webp" alt="日本語の「見つける」ページ"></td>
    <td width="50%"><img src="../../.github/assets/ja-JP/screenshot-detail.webp" alt="日本語の App 詳細ページ"></td>
  </tr>
  <tr>
    <td align="center">見つける：カテゴリ、人気の App、最近の更新</td>
    <td align="center">詳細：紹介、主要データ、インストールコマンド</td>
  </tr>
  <tr>
    <td colspan="2"><img src="../../.github/assets/ja-JP/screenshot-versions.webp" alt="日本語のバージョン履歴"></td>
  </tr>
  <tr>
    <td colspan="2" align="center">バージョン履歴：各バージョンの更新内容と Homebrew への登録日時</td>
  </tr>
</table>

## クライアントと Homebrew の連携

クライアントは自分のコンピュータでコマンドを実行するため、安全性の境界を最優先に設計しています。

- **サーバーは brew を実行しません**。インストール・更新・アンインストールは、自分の Mac 上のクライアントから実行します。
- **シェルを経由しません**：brew は引数配列で呼び出し、パッケージ名を許可リストで検証してコマンドインジェクションを防ぎます。
- **Cask のみ**：特定の App を対象にする brew コマンドには必ず `--cask` を付けます。Formula への書き込み操作は拒否します。
- **慎重なアンインストール**：Homebrew 以外で入れた App は OpenNavo 自身が削除し、その App のアンインストーラやインストールスクリプトは実行しません。関連ファイルは Bundle ID、App Group、バックグラウンド項目、インストール記録、Homebrew の zap ルールで照合します。名前が似ているだけのファイルや、ほかの App も使うファイルは既定で選択しません。各パスは削除の直前に再確認し、シンボリックリンクはたどりません。管理者権限が必要な項目はまとめて削除し、macOS のパスワード入力は 1 回だけです。Homebrew 自身の手順では別途求められることがあります。macOS に付属する App は一覧に表示しません。
- **慎重なクリーンアップ**：対象は決まった場所だけです。キャッシュ、ログ、開発ツールの場所、一時フォルダ、ダウンロードとデスクトップ、プロジェクトフォルダ、ゴミ箱、デバイスのバックアップと、アンインストールのルールで照合した残りファイルです。Docker のイメージ、ビルドキャッシュ、停止中のコンテナは Docker 自身のコマンドでのみ削除し、ボリュームは対象にしません。ブラウザの履歴、Cookie、パスワード、拡張機能には触れず、キーチェーン、パスワード管理、VPN と AI ツール、仮想マシン、iCloud Drive も対象外です。それ以外も保護リストに追加できます。各項目は削除の直前に再確認し、シンボリックリンクはたどりません。クリーンアップで管理者権限を求めることはありません。
- **慎重なファン制御**：手動の回転数は小さなヘルパーを通して設定し、ヘルパーは OpenNavo からの 5 つの決まったコマンドしか受け付けません。ファンを止める機能はなく、回転数は常にシステムの最小値と最大値の間です。OpenNavo の終了、クラッシュや 15 秒間の無応答、Mac のスリープ、書き込みの失敗のときには、ファンをシステム制御に戻します。高温でも手動に切り替えられ、チップが 95 °C 以上の状態が 10 秒続くと、手動のファンを全速にします。
- **実行を決めるのはユーザーです**：Web のディープリンクから要求された書き込み操作は、クライアントで確認してから実行します。
- **インストーラをホスティングしません**：ダウンロード元は常に上流または Homebrew の配布元です。OpenNavo はインストーラ自体を扱いません。

## アーキテクチャ

```text
opennavo/
├── apps/
│   ├── web/                Web 版（Nuxt 4 SSR）
│   ├── desktop/            macOS クライアント（Tauri 2 · Vue 3 · Rust）
│   ├── admin/              管理画面（soybean-admin v2.2.0 ベース）
│   ├── server/             Go バックエンド：公開 API・管理 API・Agent API・ワーカー
│   └── mcp/                MCP サービス（Python · FastMCP）、バックエンドの Agent API のみを呼び出す
├── packages/
│   ├── ui/                 Web 版とクライアントの共通表示コンポーネント
│   ├── tokens/             色・角丸・文字サイズの単一の定義元
│   ├── shared/             言語一覧・エラーコード・バージョン比較などの共通処理
│   └── api/                OpenAPI 契約から生成する TypeScript 型
├── tooling/
│   ├── scripts/
│   ├── config/
│   └── i18n/
├── ops/                Docker Compose、Caddy、Prometheus / Grafana、バックアップスクリプト
├── docs/
│   ├── i18n/
│   ├── security/
│   ├── legal/
│   └── design/
└── .github/
```

バックエンドは Homebrew の公開 API からカタログとインストール数を同期し、Homebrew のコミット履歴から各バージョンの登録日時を取得します。ダウンロードサイズの検出と、クライアント用のオフラインカタログのスナップショット生成も行います。API は契約を先に定義します。`apps/server/api/*.openapi.yaml` を変更し、`make gen` で Go と TypeScript のコードを生成します。

フォント以外の Cask は、現在の完全なバージョンに対応する Homebrew のコミットが 90 日以上前になると、自動的に無効とマークされます。バージョンが変わると、このローカル状態はコミット日時を再確認するまで解除されます。日時が不明な App はこのルールでは有効のままとなり、履歴取得の明示的な除外も尊重されます。Homebrew 自体の無効状態と手動の非表示は独立しています。このルールは変更のない応答を含め、カタログ同期時に適用されます。無効な項目も「無効な App を表示」やインストール済み App の記録から参照できます。

| 部分               | 技術                                                                           |
| ------------------ | ------------------------------------------------------------------------------ |
| Web 版             | Nuxt 4 · Vue 3 · UnoCSS（SSR）                                                 |
| macOS クライアント | Tauri 2 · Vue 3 · Rust · SQLite（FTS5）                                        |
| 管理画面           | soybean-admin v2.2.0 · Vue 3 · Naive UI                                        |
| バックエンド       | Go · Gin · GORM · PostgreSQL 18 · Redis 8 · asynq · S3 互換ストレージ（MinIO） |
| MCP                | Python 3.13 · FastMCP · uv                                                     |
| 契約とコード生成   | OpenAPI 3.0.3 · oapi-codegen · openapi-typescript · tauri-specta               |

## クイックスタート

### 必要な環境

- Node.js 24、pnpm 10
- Go 1.27、Rust 1.96.0（正確な Rust ツールチェーンは `apps/desktop/src-tauri/rust-toolchain.toml` に固定されています）
- Python 3.13 と [uv](https://docs.astral.sh/uv/)（MCP）
- Docker（ローカルの PostgreSQL、Redis、MinIO）
- macOS 13 以降（クライアントのみ）

### ローカルで実行

```sh
# ツールチェーンを確認し、依存関係をインストール
make setup

# ローカルのインフラを起動し、マイグレーションとシードを実行
make infra migrate seed

# Homebrew の公開 API からカタログを同期（ネットワーク接続が必要）
node tooling/scripts/tasks/server.mjs catalog-sync

# それぞれ別のターミナルで起動
make dev-api           # 公開 API と管理 API    http://localhost:8080
make dev-worker        # ワーカー：同期・翻訳・スナップショット
make dev-web           # Web 版                 http://localhost:3000
make dev-admin         # 管理画面               http://localhost:9527
make dev-desktop       # macOS クライアント（Tauri）
```

ローカルの管理者アカウントは `apps/server/.env.example` の `ADMIN_BOOTSTRAP_USERNAME` / `ADMIN_BOOTSTRAP_PASSWORD` で設定され、開発専用です。すべてのコマンドは `make help` で確認できます。

フロントエンドのみを変更する場合は、バックエンドを起動せずに開発できます。`make mock` が OpenAPI 契約に従って Prism のモック API を起動します（公開 4010、管理 4011）。続いて `MOCK=1 make dev-web` または `MOCK=1 make dev-admin` を実行します。`make dev-desktop-web` ではクライアントの画面をブラウザで実行し、システム呼び出しをモックで置き換えます。

### 設定

- `apps/server/.env.example` にバックエンドの環境変数とローカルの既定値を記載しています。API キーなどのローカルの秘密情報は、Git の対象外である `apps/server/.env.local` に保存してください。バックエンドが起動時に自動で読み込みます。開発コマンドでは、明示的なプロセス環境変数が `.env.local` より優先され、`.env.local` は `.env.example` の空でない既定値より優先されます。
- コンテンツ翻訳は初期状態では無効です。管理画面の「システム → 翻訳 AI 設定」で、OpenAI 互換 API の URL・キー・モデルを設定すると有効になります。管理画面で未設定の場合は、環境変数 `LLM_BASE_URL`（既定値 `https://api.openai.com/v1`）、`LLM_API_KEY`、`LLM_MODEL` を使用します。
- `APP_ENV` が `staging` または `prod` の場合、バックエンドは `.env.example` のサンプルの秘密情報を拒否し、公開の既定値でのデプロイを防ぎます。

### テスト

```sh
make lint test
```

OpenAPI の検証、Go / TypeScript / Rust / Python の静的検査と単体テスト、各インターフェースの対応言語すべての文言の完全性チェック（ウェブサイトと macOS クライアントは 10 言語、管理画面は 6 言語）を実行します。ブラウザの E2E テストは `make e2e` で実行できます。

> [!IMPORTANT]
> 開発・テスト中に自分のコンピュータで実際の `brew install`、`upgrade`、`uninstall`、`cleanup`、`update` を実行しないでください。クライアントの書き込み操作はモック brew でテストします。`OPENNAVO_BREW_PATH` に `apps/desktop/src-tauri/tests/fake-brew/brew` を指定してください。

## デプロイ

本番環境の構成は `ops/docker-compose.prod.yml` です。API と Web 版をそれぞれ 2 レプリカで実行し、ワーカー・Redis・Caddy を含みます。PostgreSQL（`local-db`）、Prometheus / Grafana / asynqmon（`monitoring`）、毎日のバックアップ（`backup`）は必要な profile で有効にします。

コピーして使えるイメージのビルド、外部 `COMPOSE_ENV_FILES`、データベースと S3 の初期化、バックアップと復元、profile、更新手順は[デプロイガイド](../../ops/GUIDE.md)をご覧ください。

## 開発への参加

詳しい手順は [CONTRIBUTING.md](../../.github/CONTRIBUTING.md) をご覧ください。

Issue と Pull Request を歓迎します。作業前に以下をご確認ください。

- **契約を先に定義**：API の変更はまず `apps/server/api/*.openapi.yaml` に反映し、`make gen` の後で実装を変更します。生成ファイルを手で編集しないでください。
- **デザイントークン**：色・角丸などは `packages/tokens/tokens.json` を使い、コンポーネントに色を直接記述しないでください。
- **データベース**：新しいマイグレーションファイルを追加し、既存のファイルは変更しないでください。
- **文言**：ユーザー向けのテキストは言語ファイルに入れ、各画面の対応言語すべて（ウェブサイトと macOS クライアントは 10 言語、管理画面は 6 言語）を用意してください。`make lint` が完全性を検証します。
- **コミットメッセージ**：[Conventional Commits](https://www.conventionalcommits.org/en/) に従います。例：`feat(desktop): …`、`fix(server): …`。
- コミット前に `make lint test` が通ることを確認してください。

## セキュリティ

脆弱性は **admin@opennavo.com** へ非公開でご報告ください。対応バージョンと任意で利用できる GitHub の非公開報告窓口は [SECURITY.md](../../.github/SECURITY.md) をご覧ください。脆弱性の公開 Issue は作成しないでください。

## ライセンス

本プロジェクトは [GNU General Public License 第3版のみ（GPL-3.0-only）](../../LICENSE) で公開されています。コミット `5fa4056` 以前（同コミットを含む）に公開されたバージョンは引き続き Apache-2.0 で利用できます。第三者の表記と以下の追加条項は [NOTICE](../../NOTICE) をご覧ください。依存関係の根拠資料と配布手順は [THIRD_PARTY.md](../legal/THIRD_PARTY.md) に記載しています。

NOTICE に記載した GPLv3 第7(e)条に基づく追加条項により、OpenNavo の名称やロゴに関する商標権は許諾されません。第7(c)条に基づき、変更版を配布する際は原版と明確に区別し、別の名称とアイコンを使用し、Bundle ID、更新用公開鍵、更新 URL を変更してください。正確な出典表記は保持してください。Homebrew は Homebrew プロジェクトの商標であり、OpenNavo は公式の関連製品ではありません。各アプリの名称とアイコンは各権利者に帰属します。

[デプロイガイド](../../ops/GUIDE.md) · [貢献ガイド](../../.github/CONTRIBUTING.md) · [セキュリティポリシー](../../.github/SECURITY.md) · [サードパーティライセンス](../legal/THIRD_PARTY.md)
