# 単一バイナリ（`cli/`）

`cli/` は、Kekkai のツールチェーンを 1 つの実行ファイル `kek` として配るための Rust のホストです。

- コンパイラ（`compiler/` を `./kek` でビルドした WasmGC + WASI のモジュール）、Workers のランタイム（`js/kekkai_runtime.js`）、core ライブラリのソース（`lib/core`、LSP の定義ジャンプ用）を `include_bytes!` で埋め込みます。
- wasmtime をライブラリとして組み込み、コンパイラも `kek run` のプログラムも同じプロセス内で動かします。設定は `./kek` と同じです（WasmGC、copying collector、コンパイラの GC ヒープは初期 1GiB、`/` をそのまま preopen、`PWD` を渡す）。
- コンパイラは初回にプリコンパイルし、`$KEK_CACHE`（既定は `$XDG_CACHE_HOME/kek` か `~/.cache/kek`）の `modules/` にキャッシュします。初回は約 2 秒かかり、2 回目以降はすぐに起動します。
- `kek build`／`kek run` は `./kek` と同じ 2 段のビルドキャッシュを使います。1 段目は入力のダイジェストによるアクションキャッシュ、2 段目は定義ハッシュによる `-cache` です。置き場所は `$KEK_CACHE/build/` です。

`./kek`（シェルスクリプト）はこれからも、コンパイラ自身を開発するためのランチャーです。ソースからコンパイラを作り直す処理や bootstrap の管理はこちらにしかありません。

## 対応しているコマンド

| コマンド | 単一バイナリ |
|---|---|
| `check` `ir` `build` `run` `fmt` `fix` `caps` `search` `hash` `config` `assure` `merge` `lsp` ほか、コンパイラにそのまま渡すもの | ○ |
| `similar` `complexity` `affected`（`-diff <rev>` を含む。`git` が要ります） | ○ |
| `similar -semantic` | ×（`kek test` を使うため） |
| `test` `cover` `mutate` `daemon` | ×（まだ `./kek` だけ） |
| リモートキャッシュ（`KEK_REMOTE_CACHE`） | ×（ローカルのキャッシュだけ） |
| `bootstrap-check` `bootstrap-update` `stage` | リポジトリの `./kek` だけ |

`test`／`cover`／`mutate` は、テストごとにプロセスを起動して並列に動かす部分（`xargs -P` や awk）を Rust に移植する必要があります。次の段階で対応します。

## ビルド

Rust のバージョンは `cli/rust-toolchain.toml` で決めています（wasmtime 49 には 1.96 以上が必要です）。

```sh
mise run dist        # scripts/dist.sh：./kek stage のコンパイラを cli/embed/ に置き、cargo build --release
mise run dist-test   # 対応しているスイートを KEK=cli/target/release/kek で実行（tests/run.sh）
```

`KEK_COMPILER_WASM=<module.wasm>` を指定すると、そのモジュールを埋め込みます。`kek version` は、バイナリのバージョン、ビルドしたコミット、埋め込んだコンパイラのハッシュを表示します。

## リリース

`v*` のタグを push すると `.github/workflows/release.yml` が動き、次の順に進みます。

1. Linux で `./kek bootstrap-check` を通し、`./kek stage` のコンパイラを取り出します。コンパイラは WebAssembly なので、全ターゲットで同じものを使えます。
2. ターゲットごとにネイティブのランナーでビルドし、スモークテスト（`run` の出力と `check`）を通します。ターゲットは `aarch64-apple-darwin`（macos-14）、`x86_64-unknown-linux-gnu`（ubuntu-22.04、glibc 2.35）、`aarch64-unknown-linux-gnu`（ubuntu-22.04-arm）です。
3. `kek-<target>.tar.gz` と `.sha256` を GitHub のリリースに載せます。

`scripts/install.sh` は OS とアーキテクチャから対応するアセットを選び、チェックサムを確かめてから `~/.local/bin/kek` に置きます（`KEK_INSTALL_DIR`、`KEK_VERSION` で変更できます）。
