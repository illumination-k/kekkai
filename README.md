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
| `kek build [-o dir] <file>` | `#[main]` なら WASI のコマンド、`#[handler]` なら Workers 向けモジュール（WasmGC + `worker.js`）を出力。`module.wasm` はプログラムの定義ハッシュをキーにキャッシュする（コメントや整形だけの変更では再コンパイルしない） |
| `kek run <file> [args...]` | `#[main]` のプログラムをビルドして wasmtime で実行 |
| `kek fmt [-w] [-check] <paths>` | 正準フォーマット（4 スペース、rustfmt 風）。コメントは保持。ディレクトリは `*.kek` を再帰的に探す。`-w` で上書き、`-check` は差分のあるファイルを列挙して終了コード 1 |
| `kek test [-run re] [-j n] [-json] <file>` | `#[test]` 関数をモックの capability で実行（テストごとに別プロセス、並列）。引数を取るテストはプロパティベーステスト。結果は定義ハッシュでキャッシュし、変更の影響を受けたテストだけを実行する |

### テスト（`kek test`）

テストは `#[test]` を付けた普通の関数です。戻り値は `()`・`Bool`・`Result<(), String>` のいずれかで、`false` か `Err(msg)` を返すと失敗です。引数には capability と生成できる値を取れます。capability にはランナーがテストごとに新しい**モック**を渡します。

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

capability を受け取らないテストは純粋なので hermetic で、出力に `pure: hermetic, cacheable` と表示されます。

#### プロパティベーステスト

capability 以外の引数を取るテストはプロパティです。ランナーが引数を生成して 100 ケース（`-cases n`、テストごとには `#[test(cases = N)]`）実行し、失敗した入力を最小の反例まで縮めて報告します。

```kek
#[test]
fn reverse_twice_is_identity(xs: Vec<Int>) -> Bool {
    same(reverse(reverse(xs)), xs)
}

#[test]
fn shapes_are_small(s: Shape) -> Bool {   // Shape は自前の enum
    area(s) < 12
}
```

```
test shapes_are_small ... FAILED (pure: hermetic, cacheable; 0.4ms)
    returned false
    counterexample: shapes_are_small(s = Circle(Point { x: 0, y: 0 }, 2))
    found: case 14 of 100, shrunk in 3 steps from shapes_are_small(s = Circle(Point { x: 0, y: -11 }, 6))
    reproduce: kek test -seed 0 -run '^shapes_are_small$'
```

- 生成できる型は `Int`・`Bool`・`String`・`()`・`Vec<T>`・`Option<T>`・`Result<T, E>`・タプルと、フィールドがそれらからなる自前の struct・enum（generic・再帰的なものも可）。関数・`&Tx`・`Request` などのホストの型・`HashMap` などのライブラリの型は型検査で拒否します。
- 入力は `-seed` とテスト名から作る擬似乱数で決まり、同じシードなら同じケースを再現します。ケースが進むほど大きくなり、0・±1・`Int` の最大最小・空文字列・空の `Vec`・非 ASCII 文字に偏らせています。
- 縮小は貪欲法です（整数は 0 へ、文字列と `Vec` は区間の削除と要素の縮小、`Some` は `None` へ、struct と enum はフィールドごと、再帰的な enum は部分値へ）。実行回数の上限は 2000 回です。
- ケースごとにモックを新しくします（`&Db` は空か `-db` の内容、ログは空、`&Random` は同じ系列）。反例はそれだけで再現し、失敗時に表示するログは反例の実行のものです。
- トラップはプロセスを終わらせるので縮小しません。代わりに直前に実行しようとした入力を `last input:` として表示します。

#### キャッシュと並列実行

モックはすべて決定的なので、テストの結果は「コンパイラ・テストの定義ハッシュ（`kek hash` の trans。到達できる定義と型宣言を含む）・`-seed`・`-clock`・`-net`/`-db` のファイルの内容・ケース数」で決まります。`kek test` は結果を `.kek-cache/test/` に保存し（一時ファイルに書いてからリネーム）、キャッシュにないテストだけをビルドして実行します。何も変えていなければコンパイルせずに結果を `(cached)` 付きで再表示し（失敗の出力も再生）、定義を変えるとそれに依存するテストだけが再実行されます。コメントや整形だけの変更ではハッシュは変わりません。`-no-cache` か `KEK_TEST_CACHE=0` で無効にできます。

テストは `-j n`（既定は CPU 数）個ずつ並列に実行し、結果は宣言順に表示します。`-json` は結果を JSON で出力します（形式は [docs/tooling.md](docs/tooling.md)）。

オプションは `./kek test [-run re] [-seed n] [-cases n] [-j n] [-json] [-no-cache] [-clock ms] [-net f.json] [-db f.json] <file|dir>`（`-run` は拡張正規表現）。コンパイラの `test-build`（`compiler/testrun.kek`）がテストを発見し、テスト名で 1 つを実行する `#[main]` を合成してビルドします。プロパティの生成・縮小・表示の関数も型ごとに合成します（`compiler/pbt.kek`）。モックは prelude（`lib/prelude/test.kek`・`lib/prelude/prop.kek`）にあり、`./kek` がテストごとに wasmtime のプロセスを起動します。トラップ（スタックの使い切りなど）はそのテストだけの失敗になります。

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
