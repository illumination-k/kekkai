# Self-hosting

Kekkai のコンパイラは Kekkai で書かれている（`compiler/`）。コンパイラ自身も WasmGC にコンパイルされ、Node 上で動く。

## ブートストラップ

- `bootstrap/kek.wasm` + `bootstrap/kekkai_meta.js`：コンパイラ自身をコンパイルした WasmGC モジュール（Zig と同じ方式）。
- `./kek`（`js/kek.mjs`）は bootstrap で現在の `compiler/` をビルドし（stage1）、そのコンパイラでコマンドを実行する。
  結果は `.kek-cache/stage-<hash>/` にキャッシュされる。キーは bootstrap とソースのハッシュで、一時ディレクトリに書いてからアトミックにリネームするため、ロックは要らない。
- `./kek bootstrap-check`：stage1 で自分自身をもう一度ビルドし（stage2）、stage1 == stage2（不動点）を確認する。CI でも実行する。
- `./kek bootstrap-update`：不動点を確認したうえで `bootstrap/` を更新する。

コンパイラ自身に新しい言語機能を使わせるときは、先にその機能を実装し、`bootstrap-update` してから使う。

## 規約

- `compiler/` は 1 つのプログラムで、ディレクトリ内の全 `.kek` が 1 つの名前空間を共有する。モジュールがないので、名前にコンポーネントの接頭辞を付ける。
  - フロントエンド：`Tok*`, `Ast*`, `lex_*`, `parse_*`, `ast_*`
  - 型検査：`Ty*`, `chk_*`
  - lowering：`lower_*`
  - IR：`Ir*`, `ir_*`
  - バックエンド：`Wasm*`, `wasm_*`, `cg_*`
  - JSON：`Json*`, `json_*`
  - ツール：`fmt_*`, `caps_*`, `search_*`, `diag_*`, `irjson_*`, `glue_*`, `testrun_*`
- AST のノードは識別用に `id: Int` を持つ。Map のキーは Int か String に限られ、ポインタの同一性もないためである。
- 出力は決定的にする。型・import・関数・ローカル・文字列リテラル表の順序は固定で、不動点の検査がこれに依存する。

## サブコマンド

エントリは `compiler/main.kek` の `#[main]`。

| コマンド | 内容 |
| --- | --- |
| `check [-json] <path>` | 型検査（`-json` は終了位置・phase・未使用 capability の lint 付き） |
| `ir [-json] <path>` | IR のテキスト／JSON（JSON は Lean 参照インタプリタの入力） |
| `build <path> <outdir> [-target d1\|do]` | module.wasm と kekkai_meta.js。`#[handler]` なら worker.js と wrangler.toml も出力する（既存の wrangler.toml は上書きしない） |
| `caps [-json] <path>` | 各関数の capability（＝起こしうる副作用） |
| `search [-json] [-limit n] '<sig>' [path]` | 型によるシグネチャ検索 |
| `fmt [-w\|-check] <paths>` | 正準フォーマット |
| `test-build <path> <outdir> [-list \| -run name...]` | `kek test` のテスト発見とハーネスのビルド（ランナーは `js/kek_test.mjs`） |
| `lex <file>` / `ast <file>` | トークン列と構文木のダンプ |
| `ir2wasm <ir.json> <out.wasm> [<meta.json>]` | IR の JSON から WasmGC を生成する |

`<path>` はファイルかディレクトリで、ディレクトリなら中の `*.kek` を名前順に読む。

## テスト

`node tests/run.mjs`（`mise run test`）が以下をまとめて実行する。

- 不動点の検査
- `check` の診断
- `run`
- E2E とストアアダプタの適合テスト
- Lean 参照インタプリタとの差分テスト
- `fmt` の往復
- workerd
- `tests/*.test.mjs`
