# 単一バイナリ（`cli/`）

`cli/` は、Kekkai のツールチェーンを 1 つの実行ファイル `kek` として配るための Rust のホストです。

- コンパイラ（`compiler/` を `./kek` でビルドした WasmGC + WASI のモジュール）、Workers のランタイム（`js/kekkai_runtime.js`）、core ライブラリのソース（`lib/core`、LSP の定義ジャンプ用）を `include_bytes!` で埋め込みます。
- wasmtime をライブラリとして組み込み、コンパイラも `kek run` のプログラムも同じプロセス内で動かします。設定は `./kek` と同じです（WasmGC、copying collector、コンパイラの GC ヒープは初期 1GiB、`/` をそのまま preopen、`PWD` を渡す）。
- コンパイラは初回にプリコンパイルし、`$KEK_CACHE`（既定は `$XDG_CACHE_HOME/kek` か `~/.cache/kek`）の `modules/` にキャッシュします。初回は約 2 秒かかり、2 回目以降はすぐに起動します。
- キャッシュの構成は `./kek` と同じです。ビルド（入力のダイジェストと定義ハッシュの 2 段）、テストの一覧と結果、cover、mutate、daemon のいずれも `$KEK_CACHE` の下に置きます。

`./kek`（シェルスクリプト）はこれからも、コンパイラ自身を開発するためのランチャーです。ソースからコンパイラを作り直す処理や bootstrap の管理はこちらにしかありません。

## 対応しているコマンド

`./kek` のコマンドはすべて使えます。例外はリポジトリの保守用コマンド（`bootstrap-check`、`bootstrap-update`、`stage`）だけです。`-diff <rev>`／`-affected <rev>` を使うときは `git` が、リモートキャッシュ（`KEK_REMOTE_CACHE`）を使うときは `curl` が必要です。どちらも `./kek` と同じです。

`./kek` ではシェルスクリプトの部分（awk、`xargs -P`、`tail -f`）が担っていた処理を、`cli/src/` で次のように置き換えました。

| `./kek` | 単一バイナリ |
|---|---|
| テストごとに wasmtime のプロセスを起動し、`-j` 個を並列に実行 | 同じプロセスのスレッドで、`--batch` のインスタンスを `-j` 個並べて実行（`testcmd.rs`、`mutate.rs`、`cover.rs`）。トラップや時間切れはそのインスタンスだけを終わらせ、残りは新しいインスタンスで続けます。この手順は `./kek` と同じです |
| `wasmtime -W timeout=…` | epoch interruption（10ms 刻み。`wasm.rs`）。時間切れの表示は同じく `wasm trap: interrupt` です |
| 結果の集計（awk） | 同じ書式のログを Rust で集計します。出力・JSON・キャッシュのエントリは `./kek` と同じ形です |
| daemon（`tail -f req.log \| wasmtime … serve`） | `kek __daemon <dir>` が自分自身を常駐させ、`req.log` に追記された内容をパイプ経由でコンパイラの標準入力に渡します（`daemon.rs`）。リクエストの形式（`req.log`／`out.log`／`resp-<seq>`）は同じで、タブや改行を含む引数もエスケープして送れます |

テストのスイートは `KEK=<path>` で実行する kek を、`KEK_CACHE` でそのキャッシュの場所を切り替えられます。`mise run dist-test` は、bootstrap を除く全スイートをこのバイナリで実行します。

## ビルド

Rust のバージョンは `cli/rust-toolchain.toml` で決めています（wasmtime 49 には 1.96 以上が必要です）。

```sh
mise run dist        # scripts/dist.sh：./kek stage のコンパイラを cli/embed/ に置き、cargo build --release
mise run dist-test   # bootstrap を除く全スイートを KEK=cli/target/release/kek で実行（tests/run.sh）
```

`KEK_COMPILER_WASM=<module.wasm>` を指定すると、そのモジュールを埋め込みます。`kek version` は、バイナリのバージョン、ビルドしたコミット、埋め込んだコンパイラのハッシュを表示します。

## リリース

`v*` のタグを push すると `.github/workflows/release.yml` が動き、次の順に進みます。

1. Linux で `./kek bootstrap-check` を通し、`./kek stage` のコンパイラを取り出します。コンパイラは WebAssembly なので、全ターゲットで同じものを使えます。
2. ターゲットごとにネイティブのランナーでビルドし、スモークテスト（`run` の出力と `check`）を通します。ターゲットは `aarch64-apple-darwin`（macos-14）、`x86_64-unknown-linux-gnu`（ubuntu-22.04、glibc 2.35）、`aarch64-unknown-linux-gnu`（ubuntu-22.04-arm）です。
3. `kek-<target>.tar.gz` と `.sha256` を GitHub のリリースに載せます。

`scripts/install.sh` は OS とアーキテクチャから対応するアセットを選び、チェックサムを確かめてから `~/.local/bin/kek` に置きます（`KEK_INSTALL_DIR`、`KEK_VERSION` で変更できます）。
