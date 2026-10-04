# Kekkai

Kekkai（結界）は、サーバーサイドの典型的なバグ（トランザクション不整合、隠れた副作用など）を型検査で排除するための言語です。Rust 風の構文（拡張子 `.kek`）で書き、コンパイラ `kek` は **Kekkai 自身で書かれており（セルフホスト、`compiler/`）**、WasmGC に変換して Cloudflare Workers で動かします。`#[main]` のプログラムは WASI のコマンドになり、コンパイラ自身もそうして wasmtime 上で動きます。

- **capability 渡し**：`&Log` `&Net` `&Db` `&Clock` `&Random` を引数で受け取らない関数は副作用を持てない（第二級値なので保存も返却もできない）
- **線形なトランザクション**：`db.transaction(|tx| ...)` の `Tx` は必ず一度だけ commit / rollback される。トランザクション内で取り消せない副作用は書けない（`tx.outbox` で commit 後に送る）
- **コア計算の健全性を Lean で証明**（`lean/`）

設計は [docs/design.md](docs/design.md) を参照してください。

## クイックスタート

ツールチェーン（wasmtime、workerd、wasm-tools、Lean）は [mise](https://mise.jdx.dev/) で揃えます。

```sh
mise install          # mise.toml のツールを入れる
./kek check testdata/e2e/bank.kek
./kek test testdata/test/counter.kek
./kek run testdata/run/recursive_enum.kek
./kek build -o out testdata/e2e/bank.kek   # out/ に worker.js・module.wasm・wrangler.toml
scripts/dev.sh testdata/e2e/bank.kek       # workerd でローカルに配信
```

`./kek` はシェルスクリプトで、`bootstrap/kek.wasm`（コンパイラ自身をコンパイルした WasmGC + WASI のモジュール）で現在の `compiler/` をビルドし（`.kek-cache/` にキャッシュ）、そのコンパイラを wasmtime で実行します。詳しくは [bootstrap/README.md](bootstrap/README.md) と [docs/selfhost.md](docs/selfhost.md)。

生成されるモジュールは自己完結しています。文字列・`Vec`・`Response` などの組み込み操作は Kekkai で書いたランタイムの prelude（`lib/prelude`）として、`HashMap`・イテレータ・比較やハッシュの trait は同じく Kekkai で書いた core ライブラリ（`lib/core`）として一緒にコンパイルされます。外とつながるのは capability の操作だけです。

- `#[main]`：WASI のコマンド（`_start`）。ファイル・ログ・時計・乱数は WASI で実装されています
- `#[handler]`：Workers のモジュール。capability の操作だけを JS のランタイム（`js/kekkai_runtime.js`）から import します

## コマンド

| コマンド | 内容 |
| --- | --- |
| `kek check <file>` | 型検査（capability、エフェクト、トランザクション） |
| `kek caps <file>` | 各関数が受け取る capability（＝起こしうる副作用）の一覧 |
| `kek ir [-json] <file>` | 中間表現を表示（`-json` は Lean 参照インタプリタの入力形式） |
| `kek build [-o dir] <file>` | `#[main]` なら WASI のコマンド、`#[handler]` なら Workers 向けモジュール（WasmGC + `worker.js`）を出力 |
| `kek run <file> [args...]` | `#[main]` のプログラムをビルドして wasmtime で実行 |
| `kek fmt [-w] [-check] <paths>` | 正準フォーマット（4 スペース、rustfmt 風）。コメントは保持。ディレクトリは `*.kek` を再帰的に探す。`-w` で上書き、`-check` は差分のあるファイルを列挙して終了コード 1 |
| `kek test [-run re] <file>` | `#[test]` 関数をモックの capability で実行（テストごとに別プロセス） |
| `kek cover [-json] [-lcov f] <file>` | テストの行・分岐カバレッジ（AST に計測を埋め込む。lcov 出力、`[cover] min_line`） |
| `kek mutate [-json] [-base p] [-diff rev] <file>` | ミューテーションテスト（型の付く変異体だけ。型で検出された変異体を別に数える。結果はキャッシュ） |

### テスト（`kek test`）

テストは `#[test]` を付けた普通の関数です。戻り値は `()`・`Bool`・`Result<(), String>` のいずれかで、`false` か `Err(msg)` を返すと失敗です。引数には capability だけを取れ、ランナーがテストごとに新しい**モック**を渡します。

| capability | モック |
| --- | --- |
| `&Log` | 出力を記録（失敗時に表示） |
| `&Clock` | 固定時刻（既定 2026-01-01T00:00:00Z、`-clock ms`） |
| `&Random` | シード付き乱数（`-seed n`、テスト名と混ぜる） |
| `&Db` | インメモリの `MemoryStore`（`-db seed.json` で初期値）、outbox は記録 |
| `&Net` | ネットワークなし。`-net responses.json`（`{"GET url": "body"}`）の応答だけ返す |

```kek
#[test]
fn key_format() -> Bool {
    counter_key("home") == "visits:home"
}

#[test]
fn visits_are_counted(db: &Db, log: &Log) -> Result<(), String> {
    match visit(db, log, "home") {
        Ok(1) => Ok(()),
        _ => Err("expected 1"),
    }
}
```

capability を受け取らないテストは純粋なので hermetic で、出力に `pure: hermetic, cacheable` と表示されます（定義ハッシュをキーにしたキャッシュは今後の課題）。

オプションは `./kek test [-run re] [-seed n] [-clock ms] [-net f.json] [-db f.json] <file|dir>`（`-run` は拡張正規表現）。コンパイラの `test-build`（`compiler/testrun.kek`）がテストを発見し、テスト名で 1 つを実行する `#[main]` を合成してビルドします。モックは prelude（`lib/prelude/test.kek`）にあり、`./kek` がテストごとに wasmtime のプロセスを起動します。トラップ（スタックの使い切りなど）はそのテストだけの失敗になります。

### カバレッジ（`kek cover`）

```sh
./kek cover testdata/cover/shapes.kek
./kek cover -lcov coverage.lcov -json testdata/cover/shapes.kek
```

コンパイラの `cover-build`（`compiler/cover_walk.kek`）が型検査の前に AST へ計測（`__cov_hit(k);`）を埋め込みます。計測点は関数の入口、`if` の両方の枝（`else` がなくても）、`match` の各アーム、ループとクロージャの本体、そして `return`・`?`・`break`・`continue` で抜けうる文の直後です。計測は prelude の純粋な関数を呼ぶだけなので、型・capability・`Tx` の線形性・非同期化は変わりません（`tests/suites/cover.sh` は計測したプログラムの出力が変わらないことを確かめます）。

テストは `kek test` と同じハーネスで 1 つずつ別プロセスで実行し、通った計測点を `KEK_COVER_OUT` のファイルに書き出します。結果は関数・ファイルごとの行と分岐のカバレッジ、未カバーの行、`-json`（計測点ごとに通ったテスト）、`-lcov`（エディタや CI 向け）です。`#[test]` 関数は計測せず、`#[rare]` の関数は別に表示して集計から除きます。テストごとの結果はコンパイラ・計測点の表・テストの `trans` ハッシュをキーに `.kek-cache/cover/` にキャッシュします。`kekkai.toml` の `[cover] min_line = 80` を下回るか、失敗したテストがあると終了コード 1 です。

### ミューテーションテスト（`kek mutate`）

```sh
./kek mutate testdata/mutate/calc.kek
./kek mutate -diff HEAD~1 -json src/       # 変更された定義だけ
```

型検査済みの AST から、型の付く変異体だけを作ります（`compiler/mutate_gen.kek`）。

| 種類 | 変異 |
| --- | --- |
| 算術 | `Int` の `+`↔`-`、`*`↔`/`、`%`→`*` |
| 比較 | 境界（`<`↔`<=`、`>`↔`>=`）と否定（`<`→`>=`、`==`↔`!=`） |
| 論理 | `&&`↔`\|\|`、`if`・`while` の条件の否定、`!x`・`-x` → `x` |
| リテラル | 整数 n → n+1・0、真偽値の反転、文字列 → `""` |
| 文・結果 | 呼び出しや代入の文の削除、関数の結果を `0`・`""`・`None`・`Vec::new()` に（`Bool` は否定） |

文の削除や結果の置き換えは 1 つずつ型検査し、通らないものを**型で検出**（killed by types）として別に数えます。たとえば `tx.commit()?;` の削除は `Tx` の線形性検査で弾かれます。型で検出された割合は、型システムがどれだけバグを防いでいるかの指標です。

残りの変異体は**ミュータントスキーマ**として 1 つのモジュールにまとめます。各箇所は `__mut_iop(k, x, y, op, alt)` や `if __mut_on(k) { 変異 } else { 元 }` のような prelude の呼び出しになり、実行時に環境変数 `KEK_MUTANT=k` で 1 つを選びます。まず変異なしで各テストを実行して、どのテストがどの変異箇所に到達するかを記録し、変異体ごとに到達するテストだけを、どれかが失敗するまで実行します（wasmtime のプロセスを並列に起動）。結果は検出（killed）、生存（survived）、時間切れ（timeout、既定は 1 秒 + 最も遅いテストの 10 倍、`-timeout 2s`）、未到達（no coverage）です。スコアは (killed + timeout) / (killed + timeout + survived + no coverage) で、`kekkai.toml` の `[mutate] min_score = 80` を下回ると終了コード 1 です。

`-base <file|dir>` か `-diff <git-rev>` を付けると、定義ハッシュ（`hash`）が変わった定義と新しい定義だけを変異させます。ビルドはソースの内容で、変異体ごとの結果は「変異体（関数の `trans` ハッシュ + 関数内の位置 + 変異）× テストの `trans` ハッシュ」で `.kek-cache/mutate/` にキャッシュするので、変更のない 2 回目の実行はテストを 1 つも動かしません。

## 開発

```sh
mise run test        # tests/run.sh（不動点・check・run・kek test・fmt・差分テスト・workerd での e2e）
mise run fmt         # kek fmt -w compiler lib testdata/{e2e,run,test} examples tests/difftest
mise run fmt-check   # フォーマット検査
mise run lean        # Lean の証明をビルド
mise run ci          # test・fmt-check・lean をまとめて実行（CI と同じ）
./kek bootstrap-update  # 不動点を確認して bootstrap/ を更新
```

CI（`.github/workflows/ci.yml`）は `jdx/mise-action` でツールを入れ、`mise run lean`・`tests/run.sh --short`・`mise run fmt-check` を実行します。
