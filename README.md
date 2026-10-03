# Kekkai

Kekkai（結界）は、サーバーサイドの典型的なバグ（トランザクション不整合、隠れた副作用など）を型検査で排除するための言語です。Rust 風の構文（拡張子 `.kek`）で書き、コンパイラ `kek`（Go 製）が WasmGC に変換して Cloudflare Workers で動かします。

- **capability 渡し**：`&Log` `&Net` `&Db` `&Clock` `&Random` を引数で受け取らない関数は副作用を持てない（第二級値なので保存も返却もできない）
- **線形なトランザクション**：`db.transaction(|tx| ...)` の `Tx` は必ず一度だけ commit / rollback される。トランザクション内で取り消せない副作用は書けない（`tx.outbox` で commit 後に送る）
- **コア計算の健全性を Lean で証明**（`lean/`）

設計は [docs/design.md](docs/design.md) を参照してください。

## クイックスタート

ツールチェーン（Go、Node、wasm-tools、Lean、wrangler）は [mise](https://mise.jdx.dev/) で揃えます。

```sh
mise install          # mise.toml のツールを入れる
mise run build        # bin/kek をビルド
bin/kek check testdata/e2e/bank.kek
bin/kek test testdata/test/counter.kek
bin/kek build -o out testdata/e2e/bank.kek   # out/ に worker.js・module.wasm・wrangler.toml
cd out && wrangler dev
```

## コマンド

| コマンド | 内容 |
| --- | --- |
| `kek check <file>` | 型検査（capability、エフェクト、トランザクション） |
| `kek caps <file>` | 各関数が受け取る capability（＝起こしうる副作用）の一覧 |
| `kek ir [-json] <file>` | 中間表現を表示（`-json` は Lean 参照インタプリタの入力形式） |
| `kek build [-o dir] <file>` | Workers 向けモジュール（WasmGC + JS グルー）を出力 |
| `kek fmt [-w] [-check] <paths>` | 正準フォーマット（4 スペース、rustfmt 風）。コメントは保持。ディレクトリは `*.kek` を再帰的に探す。`-w` で上書き、`-check` は差分のあるファイルを列挙して終了コード 1 |
| `kek test [-run re] <file>` | `#[test]` 関数を Node 上の WasmGC で実行 |

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

## 開発

```sh
mise run test        # go test ./...（単体・ゴールデン・Node 上の e2e・差分テスト）
mise run fmt         # gofmt と kek fmt -w
mise run fmt-check   # フォーマット検査
mise run lean        # Lean の証明をビルド
mise run ci          # vet・test・fmt-check・lean をまとめて実行（CI と同じ）
```

CI（`.github/workflows/ci.yml`）は `jdx/mise-action` でツールを入れ、`go vet`・`go test ./...`・`mise run fmt-check`・`mise run lean` を実行します。
