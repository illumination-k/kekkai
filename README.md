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
| `kek assure plan\|apply\|check <dir>` | 保証の台帳 `kekkai.assure.lock`：保証の変化（強化／変更／弱化／新しい前提）を `kekkai.toml` のポリシーで判定し、承認してロックを更新、CI でドリフトを検出（[docs/assure.md](docs/assure.md)） |
| `kek similar [-json] [-threshold pct] [-all] [-tests] [-base path \| -diff rev] <file\|dir>` | 重複・類似コードの検出。見つかれば終了コード 1（CI で強制できる） |

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

### 保証の台帳（`kek assure`）

コンパイラが確立している保証（capability の集合、`&Net`・`tx.outbox` の通信先、トランザクションの線形性、テストからの到達）と、コード側の前提（`#[allow(similar, ...)]`・`#[rare]`）を関数ごとに `kekkai.assure.lock`（JSON。git に commit する正本）へ記録します。人間はコードではなく保証の変化だけをレビューします。

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

ポリシーは `kekkai.toml` の `[net] allowed_hosts`・`[effects] forbid`・`[auto_approve]`・`[escalate]`・`[module."path"]`（厳しくする方向にだけ上書きできる）・`[assure] extends`（組織の設定を継承）で書きます。詳細は [docs/assure.md](docs/assure.md)。
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
- 書き方は違うが意味が同じコード（生成した入力で出力を比べる）の検出は今後の課題です。

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
