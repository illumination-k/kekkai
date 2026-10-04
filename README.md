# Kekkai

Kekkai（結界）は、サーバーサイドの典型的なバグ（トランザクション不整合、隠れた副作用など）を型検査で排除するための言語です。Rust 風の構文（拡張子 `.kek`）で書き、コンパイラ `kek` は **Kekkai 自身で書かれており（セルフホスト、`compiler/`）**、WasmGC に変換して Cloudflare Workers で動かします。`#[main]` のプログラムは WASI のコマンドになり、コンパイラ自身もそうして wasmtime 上で動きます。

- **capability 渡し**：`&Log` `&Net` `&Db` `&Clock` `&Random` を引数で受け取らない関数は副作用を持てない（第二級値なので保存も返却もできない）
- **線形なトランザクション**：`db.transaction(|tx| ...)` の `Tx` は必ず一度だけ commit / rollback される。トランザクション内で取り消せない副作用は書けない（`tx.outbox` で commit 後に送る）
- **個人情報の型**：`Pii<T>` に包んだ値は文字列にできず、ログ・レスポンス・ストアに渡すと型エラーになる。取り出す（格下げ）のは `mask()`・`hash()`・`expose_unchecked()` だけで、その呼び出しは `kek caps` と `kek assure` に記録される
- **篩型（refinement types）**：`where 0 <= i, i < v.len()` のような線形の事前条件・事後条件（`result`）と、`type Port = Int where 0 < self && self < 65536;` のような述語付きの型の別名。`v[i]` は範囲内であることを証明できなければ型エラーで、反例（`i = 0, v.len() = 0`）を示す。証明は自作の QF\_LIA ソルバ（Omega test、Kekkai で実装）。ゼロ除算・オーバーフローは lint（`kekkai.toml` の `[refine]` でエラーにできる）
- **冪等なハンドラ**：`#[handler(idempotent)]` のハンドラと、そこから呼ばれる関数は、冪等でない操作（`net.post`、`tx.outbox`、ファイルへの書き込み、乱数）を使えない
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
| `kek check [-v] [-json] <file>` | 型検査（capability、エフェクト、トランザクション、篩型）。篩型の違反は反例付きのエラー、ゼロ除算・オーバーフローは警告。`-v` は証明に使った事実と証明できた条件も表示 |
| `kek caps <file>` | 各関数が受け取る capability（＝起こしうる副作用）の一覧。冪等か（`idempotent`）と個人情報の格下げ（`declassify`）も表示 |
| `kek ir [-json] <file>` | 中間表現を表示（`-json` は Lean 参照インタプリタの入力形式） |
| `kek build [-o dir] <file>` | `#[main]` なら WASI のコマンド、`#[handler]` なら Workers 向けモジュール（WasmGC + `worker.js`）を出力。`module.wasm` はプログラムの定義ハッシュをキーにキャッシュする（コメントや整形だけの変更では再コンパイルしない） |
| `kek run <file> [args...]` | `#[main]` のプログラムをビルドして wasmtime で実行 |
| `kek fmt [-w] [-check] <paths>` | 正準フォーマット（4 スペース、rustfmt 風）。コメントは保持。ディレクトリは `*.kek` を再帰的に探す。`-w` で上書き、`-check` は差分のあるファイルを列挙して終了コード 1 |
| `kek test [-run re] [-j n] [-json] <file>` | `#[test]` 関数をモックの capability で実行（テストごとに別プロセス、並列）。引数を取るテストはプロパティベーステスト。結果は定義ハッシュでキャッシュし、変更の影響を受けたテストだけを実行する |
| `kek assure plan\|apply\|check <dir>` | 保証の台帳 `kekkai.assure.lock`：保証の変化（強化／変更／弱化／新しい前提）を `kekkai.toml` のポリシーで判定し、承認してロックを更新、CI でドリフトを検出（[docs/assure.md](docs/assure.md)）。篩型の証明（`refine.index_safe`・`refine.no_div_zero`・`refine.no_overflow`）も記録する |
| `kek similar [-json] [-threshold pct] [-all] [-tests] [-semantic] [-base path \| -diff rev] <file\|dir>` | 重複・類似コードの検出。見つかれば終了コード 1（CI で強制できる） |
| `kek cover [-json] [-lcov f] <file>` | テストの行・分岐カバレッジ（AST に計測を埋め込む。lcov 出力、`[cover] min_line`） |
| `kek affected [-json] -diff <rev> <file>` | git の revision からの変更で、振る舞いが変わりうる定義・走らせるべきテスト・ビルド出力が変わるかを表示（`kek test -affected <rev>` でそのテストだけ実行） |
| `kek daemon start\|stop\|status\|stats` | コンパイラを常駐させる（構文木をメモリに残し、変わったファイルだけ構文解析する） |
| `kek hash [-json] <file>` | 定義ハッシュ（α同値で正規化、`trans` は依存先と型宣言を含む）。テスト・ビルド・カバレッジ・ミューテーションのキャッシュ、`similar`、`assure` の土台 |
| `kek config` | `kekkai.toml`（プロジェクトの設定：`[similar]`・`[cover]`・`[mutate]`・`[net]` など）を JSON で表示して構文を確認 |
| `kek mutate [-json] [-base p] [-diff rev] [-shard i/n] <file>` | ミューテーションテスト（型の付く変異体だけ。型で検出された変異体を別に数える。結果はキャッシュ。シャードに分割できる） |

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

capability 以外の引数を取るテストはプロパティです。ランナーが引数を生成して 100 ケース（`-cases n`、テストごとには `#[test(cases = N)]`）実行し、失敗した入力を最小の反例まで縮めて報告します。引数の型が篩型の別名（`p: Port`）なら、述語を満たす値だけを生成します（棄却法。縮小も述語の中で行います）。

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

### 保証の台帳（`kek assure`）

コンパイラが確立している保証（capability の集合、`&Net`・`tx.outbox` の通信先、トランザクションの線形性、冪等性、テストからの到達）と、コード側の前提（`#[allow(similar, ...)]`・`#[rare]`・個人情報の格下げ `pii.declassify`）を関数ごとに `kekkai.assure.lock`（JSON。git に commit する正本）へ記録します。人間はコードではなく保証の変化だけをレビューします。

```sh
./kek assure plan app       # ロックとの差分（-json が基本の出力、-v で自動承認分も表示）
./kek assure apply app      # 自動承認分だけならそのまま更新。要レビューは -yes、弱化はさらに -reason -owner -expires が必要
./kek assure check app      # CI 用：ロックのずれ・ポリシー違反・期限切れで終了コード 1
```

```
Needs review (1):
  ! weaken effects: +Log   rate_key
    -> app/rates.kek:19  escalate: owner (apply needs -reason, -owner, -expires)

Auto-approved (3): strengthen 1, allowed host 2
```

ポリシーは `kekkai.toml` の `[net] allowed_hosts`・`[effects] forbid`・`[pii] max_declassify_per_module`／`declassify_requires`・`[auto_approve]`・`[escalate]`・`[module."path"]`（厳しくする方向にだけ上書きできる）・`[assure] extends`（組織の設定を継承）で書きます。詳細は [docs/assure.md](docs/assure.md)。
### 類似コードの検出（`kek similar`）

LLM が既存の実装を探さずに似た関数を書き足すのを防ぐためのコマンドです（設計は [docs/design.md](docs/design.md) の「類似コードの検出」、JSON は [docs/tooling.md](docs/tooling.md)）。定義ハッシュ（`compiler/defhash.kek`）の上で、次の 3 種類を報告します。

| 種類 | 意味 | 検出方法 |
| --- | --- | --- |
| `duplicate` | 名前（関数名・変数名）だけが違う | α同値で正規化した定義ハッシュが一致 |
| `literals` | 定数だけが違う | リテラルを抽象化したハッシュが一致。違うリテラルの位置と値を示し、引数化を提案 |
| `structural` | 構造が近い | ラベル列の shingle の MinHash で候補を絞り、木の編集距離（Zhang–Shasha）で類似度 = 1 − 距離 / 大きい方のノード数 を求め、閾値（既定 80%）以上を報告 |

```sh
./kek similar compiler                 # 1 件 1 ブロック：file:line・類似度・ヒント
./kek similar -json -threshold 90 src  # エージェント向けの JSON
./kek similar -diff origin/main src    # PR の CI：追加・変更された定義が関わる類似だけ
```

- 既定ではシグネチャ（capability を含む）が同じ定義どうしだけを比べます。`-all` で型をまたいで比べます。
- `#[test]` 関数（`-tests` で対象にする）、`#[derive]` が生成した実装、core と prelude は対象外です。小さすぎる定義（構文木のラベルが 16 個未満）も比べません。
- 意図的な重複は `#[allow(similar, reason = "...", owner = "...", expires = "YYYY-MM-DD")]` を付けた定義で抑制でき、JSON の `allowed` に理由とともに残ります。
- `-base <file|dir>` は基準になる古いプログラムで、同じ定義ハッシュを持つ定義は既存とみなし、新しい定義を含む指摘だけを報告します。`-diff <rev>` では `./kek` が `git archive` でその revision のプログラムを一時ディレクトリに取り出して `-base` に渡します。
- `kekkai.toml` の `[similar]` で `threshold`（%）・`min_nodes`・`max_nodes`（これより大きい木は木の編集距離の代わりにラベル列の編集距離で近似）を設定できます。フラグが優先します。
- `-semantic` を付けると、書き方は違うが意味が同じコードも探します。シグネチャが同じ純粋関数の組ごとに `a(x) == b(x)` のプロパティテストを合成し、`kek test` で 200 個の生成入力に対して比べます（`semantic`。証明ではありません。1 組の制限時間は `KEK_TEST_TIMEOUT`、既定 10s）。

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

文の削除や結果の置き換えは 1 つずつ型検査し、通らないものを**型で検出**（killed by types）として別に数えます。たとえば `tx.commit()?;` の削除は `Tx` の線形性検査で弾かれます。篩型を使う関数ではすべての変異体をこうして検査し、証明が崩れる変異体（`v[i]` の前の `i < n` を `i <= n` にするなど）も型で検出になります。型で検出された割合は、型システムがどれだけバグを防いでいるかの指標です。

残りの変異体は**ミュータントスキーマ**として 1 つのモジュールにまとめます。各箇所は `__mut_iop(k, x, y, op, alt)` や `if __mut_on(k) { 変異 } else { 元 }` のような prelude の呼び出しになり、実行時に 1 つを選びます。型が付かないかもしれない変異体（文の削除・結果の置き換え）は、まとめて型検査して失敗したグループだけを二分探索します（変異体ごとにプログラム全体を検査し直さない）。

実行は mutrim と同じ方針です。

- まず変異なしで全テストを 1 プロセスで実行し、テストごとに到達する変異箇所と**プローブの通過回数**（ticks）を記録します。
- 変異体ごとに、到達するテストだけを速い順に、どれかが失敗するまで実行します。1 つの wasmtime プロセスが多数の（変異体, テスト）を順に実行し（`--batch`）、トラップしたときだけ残りを新しいプロセスでやり直します。
- 時間切れは壁時計ではなく、そのテストの変異なしの ticks の 10 倍 + 1000 で決めます。決定的なのでキャッシュでき、遅いマシンでも結果が変わりません（壁時計の `-timeout`、既定 60 秒は保険）。
- 結果は検出（killed）、生存（survived）、時間切れ（timeout）、未到達（no coverage）です。スコアは (killed + timeout) / (killed + timeout + survived + no coverage) で、`kekkai.toml` の `[mutate] min_score = 80` を下回ると終了コード 1 です。

`-shard i/n`（または Bazel の `TEST_SHARD_INDEX` / `TEST_TOTAL_SHARDS`）で変異体を n 個に分けて別々のマシンで実行し、`-results f` で書いた結果を `-merge f0,f1,...` でまとめて 1 つのレポートにできます。

`-base <file|dir>` か `-diff <git-rev>` を付けると、定義ハッシュ（`hash`）が変わった定義と新しい定義だけを変異させます。変異体ごとの結果は「変異体（関数の `trans` ハッシュ + 関数内の位置 + 変異）× テストの `trans` ハッシュ」で `.kek-cache/mutate/` にキャッシュするので、変更のない 2 回目の実行はテストを 1 つも動かしません。

変異体 1768 個・テスト 100 個のベンチマーク（`testdata/bench/mutate_bench.kek`）で、以前の 1 プロセス 1 実行の方式の 51.7 秒が 3.9 秒（キャッシュありで 0.9 秒）になり、判定はすべて一致しました。

### アクションキャッシュと影響範囲

`kek build`・`kek test` は Bazel のアクションと同じく、入力（コンパイラとプログラムのファイル）のダイジェストで出力をキャッシュし、ソースが変わらなければコンパイラを起動しません。コメントだけの変更なら定義ハッシュで、変更の影響を受けないテストの結果はテストの `trans` ハッシュでヒットします。

```sh
./kek affected -diff origin/main src          # 変わった定義・影響を受ける定義とテスト・ビルド出力が変わるか
./kek test -affected origin/main src          # 影響を受けるテストだけ（キャッシュのない CI 向け）
KEK_REMOTE_CACHE=https://cache.example ./kek test src   # CI と手元でキャッシュを共有
```

コンパイラを起動したままにすると、ソースファイルの構文木がメモリに残り、変わったファイルだけを構文解析します（コンパイラ自身の規模で、関数本体を変えた再ビルドが約 1.8 秒から約 1.3 秒）。起動していれば `./kek` が自動で使います。

```sh
./kek daemon start     # stop / status / stats（構文解析の再利用の回数）
```

`KEK_REMOTE_CACHE` は Bazel のリモートキャッシュと同じ HTTP プロトコル（`GET`/`PUT <url>/ac/<sha256>`）で、bazel-remote（`--disable_http_ac_validation`）や `file://` の共有ディレクトリが使えます。設計と現状は [docs/parallel-build.md](docs/parallel-build.md)。

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
