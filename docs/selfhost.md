# Self-hosting

Kekkai のコンパイラは Kekkai で書かれている（`compiler/`）。コンパイラ自身も WasmGC + WASI のモジュールにコンパイルされ、wasmtime 上で動く。

## ブートストラップ

- `bootstrap/kek.wasm`：コンパイラ自身をコンパイルしたモジュール（Zig と同じ方式）。
- `./kek`（POSIX シェルのスクリプト）は bootstrap で現在の `compiler/` をビルドし（stage1）、そのコンパイラでコマンドを実行する。
  - 結果は `.kek-cache/stage-<hash>/` にキャッシュされる。キーは bootstrap、`compiler/` と `lib/prelude/` のソース、wasmtime のバージョンのハッシュ。
  - stage は `wasmtime compile` で事前にコンパイルしておく（`module.cwasm`）ので、起動は数十ミリ秒で済む。
  - 一時ディレクトリに書いてから名前を変えて置くため、ロックは要らない。
  - キャッシュがないときは、ビルドの前に `compiler/prelude_src.kek` を `lib/prelude` から作り直す。
- `./kek bootstrap-check`：stage1 で自分自身をもう一度ビルドし（stage2）、stage1 == stage2（不動点）を確認する。CI でも実行する。
- `./kek bootstrap-update`：不動点を確認したうえで `bootstrap/kek.wasm` を更新する。コンパイラが自分自身に生成するコードが変わったとき（prelude・core・コード生成の変更）は stage1 と stage2 が一致しないので、stage2 が自分自身を同じに再生成すること（stage2 == stage3）を確かめて stage2 を入れる。

コンパイラ自身に新しい言語機能を使わせるときは、先にその機能を実装し、`bootstrap-update` してから使う。

## ランタイムの prelude

生成されるモジュールは自己完結している。組み込み操作（`String`、`Response`、`TxError` など）は Kekkai で書いた prelude（`lib/prelude/*.kek`）として実装され、`build` のときにプログラムと一緒に型検査・コンパイルされる。

`HashMap`・`HashSet`・イテレータ・`PartialEq` などの trait は core ライブラリ（`lib/core/*.kek`）にある。core は prelude と違って intrinsic に頼らない普通の Kekkai のコードで、`check`・`ir` を含むすべてのコマンドでプログラムに加わる（`ir -json` にも使われた関数が入り、Lean 参照インタプリタがそのまま実行する）。コンパイラには `compiler/core_src.kek` として埋め込まれる（`./kek embed-prelude lib/core compiler/core_src.kek -as core`）。

- 組み込み操作 `recv.name`（例：`string.split`）は prelude の関数 `__recv_name` が実装する。引数はレシーバと元の引数。
- prelude は `__` で始まる intrinsic（コード生成がインラインで出す）の上に書かれている。
  - `__Chars`：文字列の UTF-16 配列
  - `__Mem`：線形メモリ
  - `__Wasi`：WASI preview 1 の関数
  - `__Js`：Workers の JS ホスト
  - `__Any`：anyref
  - `__Int`：符号なし演算
  - `S::from_any` / `S::to_any`、`__Rt::get()`：ランタイムの状態
- `__` で始まる識別子は予約されていて、ユーザーのプログラムからは書けない。
- prelude は `Response`・`TxError`・`NetError`・`IoError` を構造体として定義する。型検査器は prelude と一緒のときだけ、組み込みの不透明型をその構造体と同一視する。
- `check`・`ir` は prelude を含めない。`ir -json` はユーザーのプログラムだけの IR で、Lean 参照インタプリタの入力になる。
- prelude はコンパイラに `compiler/prelude_src.kek` として埋め込まれる（`./kek embed-prelude lib/prelude compiler/prelude_src.kek`、`mise run prelude`）。
- コード生成は到達可能な関数だけを出力するので、使わない prelude の関数はモジュールに入らない。

## ターゲット

| ターゲット | エントリ | 外とのつながり |
| --- | --- | --- |
| WASI（`#[main]`、`kek test`） | `_start`。引数を `Vec<String>` で渡し、戻り値で終了する | WASI preview 1。ファイルシステムのルートを `/` として開き、相対パスは `$PWD` から解決する。`&Net` は使えない（`NetError` を返す）。`&Db` はインメモリのストア |
| Workers（`#[handler]`） | `handler_new` / `handler_step` / `handler_result` | capability の操作だけを `js/kekkai_runtime.js` から import する。文字列は線形メモリを通して UTF-16 で受け渡す。`Response` はエクスポートされた `resp_*` 関数で読む |

文字列は WasmGC の `(array (mut i16))`。リテラルは受動データセグメントに置き、最初に使うときに作ってキャッシュする。

## 規約

- `compiler/` は 1 つのプログラムで、ディレクトリ内の全 `.kek` が 1 つの名前空間を共有する。モジュールがないので、名前にコンポーネントの接頭辞を付ける。
  - フロントエンド：`Tok*`, `Ast*`, `lex_*`, `parse_*`, `ast_*`、`#[derive]` の `derive_*`、糖衣構文の書き換え（定数・タプル構造体・構造体のようなバリアント・構造体のパターン）の `desugar_*`, `Dsg*`
  - 型検査：`Ty*`, `chk_*`（可変性の検査は `Mu*`, `mu_*`。`mut_` は mutate が使う）
  - lowering：`lower_*`
  - IR：`Ir*`, `ir_*`
  - バックエンド：`Wasm*`, `wasm_*`, `cg_*`
  - JSON：`Json*`, `json_*`
  - ツール：`fmt_*`, `fix_*`, `caps_*`, `search_*`, `diag_*`, `irjson_*`, `glue_*`, `testrun_*`, `prelude_*`
- AST のノードは識別用に `id: Int` を持つ。ポインタの同一性がないので、型検査の結果などはノードの id をキーにした `HashMap` に置く。
- 出力は決定的にする。型・import・関数・ローカル・文字列リテラル表の順序は固定で、不動点の検査がこれに依存する。

## サブコマンド

エントリは `compiler/main.kek` の `#[main]`。

| コマンド | 内容 |
| --- | --- |
| `check [-json] <path>` | 型検査（`-json` は終了位置・phase・未使用 capability の lint 付き） |
| `ir [-json] <path>` | IR のテキスト／JSON（JSON は Lean 参照インタプリタの入力） |
| `build <path> <outdir> [-target d1\|do]` | module.wasm。`#[handler]` なら kekkai_meta.js、worker.js、wrangler.toml も出力する（既存の wrangler.toml は上書きしない） |
| `caps [-json] <path>` | 各関数の capability（＝起こしうる副作用） |
| `search [-json] [-limit n] '<sig>' [path]` | 型によるシグネチャ検索 |
| `fmt [-w\|-check] <paths>` | 正準フォーマット |
| `fix [-w] <paths>` | 可変性の規則が求める `mut` を足す（[mutability.md](mutability.md)） |
| `test-build <path> <outdir> [-list \| -run name...]` | `kek test` のテスト発見とハーネスのビルド |
| `lex <file>` / `ast <file>` | トークン列と構文木のダンプ |
| `embed-prelude <dir> <out.kek> [-check]` | prelude をコンパイラに埋め込むソースを生成する |
| `prelude-check` | prelude だけを型検査する |

`<path>` はファイルかディレクトリで、ディレクトリなら中の `*.kek` を名前順に読む。

## テスト

`tests/run.sh`（`mise run test`）が以下をまとめて実行する。スイートは `tests/suites/*.sh`。

- 不動点の検査と、埋め込まれた prelude が最新であること
- `check` の診断
- `run`
- `kek test`
- エージェント向けコマンドのゴールデン
- `fmt` のゴールデンと往復
- Lean 参照インタプリタとの差分テスト
- workerd での E2E とストアアダプタの適合テスト
