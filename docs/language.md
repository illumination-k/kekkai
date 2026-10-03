# Kekkai 言語リファレンス（現行実装）

設計の背景は [design.md](design.md)。ここではセルフホストのコンパイラ（`compiler/`、`./kek`）が現在受け付ける言語を説明する。

## プログラムの構成

- 1 ファイル、または 1 ディレクトリ（中の `*.kek` すべてが 1 つの名前空間）が 1 プログラム。
- トップレベルは `struct`・`enum`・`fn` のみ。グローバル変数はない（＝暗黙の権限がない）。
- エントリポイントは次のどちらか一つ。
  - `#[handler] fn h(req: Request, db: &Db, ...) -> Response`：Workers の HTTP ハンドラ
  - `#[main] fn main(args: Vec<String>, fs: &Fs, ...) -> Int`：コマンドラインプログラム（`kek run`）
- `#[test]` 関数は `kek test` がモック capability を渡して実行する。

## 型

| 型 | 説明 |
| --- | --- |
| `Int` | 64bit 符号付き整数。演算はラップアラウンド、`x / 0 == 0`、`x % 0 == x`（panic しない） |
| `Bool`, `String`, `()` | 文字列は不変 |
| `Option<T>`, `Result<T, E>` | `Some`/`None`, `Ok`/`Err` |
| `Vec<T>` | 伸長可能な配列（参照型） |
| `Map<K, V>` | `K` は `Int` か `String`。挿入順を保つ（参照型） |
| `struct S { f: T }` | フィールドは代入可能（参照型） |
| `enum E { A, B(T, U) }` | 再帰的に定義してよい |
| `Request`, `Response`, `TxError`, `NetError`, `IoError` | ホストが提供する不透明なデータ |
| `&Log`, `&Net`, `&Db`, `&Clock`, `&Random`, `&Fs`, `Tx`, `&Tx` | capability（下記） |

型推論は関数本体の中だけで行う（関数のシグネチャは明示）。ジェネリクスは組み込み型のみ。

## capability

副作用は capability を通してしか起こせない。capability は**第二級**である。

- 関数の引数（`&Cap`）としてのみ受け取れる。変数への束縛、戻り値、構造体のフィールド、`Vec` の要素にはできない。
- 使い方はメソッドのレシーバにするか、別の関数の capability 引数に渡すかの二通り。
- capability を受け取らない関数は**純粋**（`kek caps` で一覧できる）。

| capability | 操作 |
| --- | --- |
| `&Log` | `info`, `warn`, `error` |
| `&Clock` | `now_ms() -> Int` |
| `&Random` | `int(lo, hi) -> Int` |
| `&Net` | `get(url)`, `post(url, body)` → `Result<String, NetError>` |
| `&Fs` | `read(path)`, `write(path, s)`, `write_bytes(path, Vec<Int>)` → `Result<_, IoError>` |
| `&Db` | `get(key)`, `transaction(\|tx\| ...)` |

### トランザクション

```kek
fn transfer(db: &Db, from: String, to: String, n: Int) -> Result<(), TxError> {
    db.transaction(|tx| {
        let a = tx.get(from)?.unwrap_or("0").parse_int().unwrap_or(0);
        tx.put(from, (a - n).to_string())?;   // `?` で抜けると自動で rollback
        tx.outbox("https://hooks.example/t", from);  // commit 後に送信
        tx.commit()
    })
}
```

- `Tx` は**線形**で、すべての通常経路でちょうど 1 回 `commit()` か `rollback()` を呼ばなければならない。呼んだ後に使うこと、ループ内で終わらせること、`return` の前に終わらせ忘れることは型エラーになる。
- 例外として、`?` で本体を抜ける経路では終わっていなくてよい（ランタイムが rollback する）。
- トランザクション本体の中では、取り消せない capability（`&Net`、`&Db`、`&Fs`）は見えない。効果は `tx.outbox` に積んでおけば、commit の成功後に実行される。
- 関数は `&Tx` を借りられるだけで、終わらせられない。
- バックエンドは実行時アダプタで差し替えられる（インメモリ、D1、Durable Objects、分散 KV）。`commit()` は楽観的並行制御の競合で失敗しうる。その場合 `TxError.retryable()` は true になる。

## 文と式

- `let x = e;`, `let mut x: T = e;`, `x = e;`, `s.f = e;`
- `if c { } else if d { } else { }`（式）、`match e { pat => e, ... }`（式、網羅性検査あり、ネスト可）
- `while c { }`, `for x in a..b { }`, `for x in vec { }`, `break;`, `continue;`, `return e;`
- `e?`：`Result` / `Option` の早期リターン（エラー型は一致が必要）
- 演算子：`+ - * / %`（`String` の `+` は連結）、`== != < <= > >=`、`&& || !`
- パターン：`_`、変数、整数・文字列・真偽値リテラル、`Some(p)`、`None`、`Ok(p)`、`Err(p)`、`E::V(p, ...)`、`V`

## 組み込みメソッド（抜粋）

- `Int`：`to_string`, `abs`, `min`, `max`, `bit_and`, `bit_or`, `bit_xor`, `shl`, `shr`, `ushr`
- `String`：`len`, `char_at(i) -> Option<Int>`（UTF-16）, `slice(a, b)`, `index_of`, `contains`, `starts_with`, `ends_with`, `split`, `replace`, `trim`, `to_upper`, `to_lower`, `parse_int`, `to_bytes`；`String::from_char(c)`, `String::from_bytes(v)`
- `Vec<T>`：`Vec::new()`, `push`, `get(i) -> Option<T>`, `set(i, x) -> Bool`, `pop`, `len`, `join(sep)`（`Vec<String>`）
- `Map<K, V>`：`Map::new()`, `insert`, `get -> Option<V>`, `contains`, `remove`, `len`, `keys -> Vec<K>`
- `Option`/`Result`：`is_some`, `is_none`, `is_ok`, `is_err`, `unwrap_or`
- `Request`：`method`, `path`, `segment(i)`, `query(k)`, `header(k)`, `body`
- `Response::text(status, body)`, `json`, `empty`, `no_content`, `not_found`, `bad_request`, `.with_header(k, v)`

範囲外アクセスは `Option` で表され、panic は起きない。

`for x in v` は毎回 `v.len()` を読み直す。本体で `v`（やその別名）に `push` すると終わらないので注意する。

## 実行モデル

- I/O に到達する関数は、コンパイラがステートマシンに変換する。async/await の色分けはない。
- それ以外の関数は普通の wasm 関数になる。
- 意味論の基準は Lean で書いた IR の参照インタプリタ（`lean/`）で、コンパイラとは差分テストで突き合わせる。
