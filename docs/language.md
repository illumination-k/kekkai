# Kekkai 言語リファレンス（現行実装）

設計の背景は [design.md](design.md)、generics・trait・コレクションの設計は [generics.md](generics.md)。ここではセルフホストのコンパイラ（`compiler/`、`./kek`）が現在受け付ける言語を説明する。構文と標準ライブラリの名前は Rust に合わせている。

## プログラムの構成

- 1 ファイル、または 1 ディレクトリ（中の `*.kek` すべてが 1 つの名前空間）が 1 プログラム。
- トップレベルは `struct`・`enum`・`trait`・`impl`・`fn` のみ。グローバル変数はない（＝暗黙の権限がない）。
- どのプログラムにも core ライブラリ（`lib/core`：比較・ハッシュ・`Default`・イテレータ・`HashMap`／`HashSet`）が含まれる。core の型名と trait 名は予約されている。
- エントリポイントは次のどちらか一つ。
  - `#[handler] fn h(req: Request, db: &Db, ...) -> Response`：Workers の HTTP ハンドラ
  - `#[main] fn main(args: Vec<String>, fs: &Fs, ...) -> Int`：コマンドラインプログラム（`kek run`）
- `#[test]` 関数は `kek test` がモック capability を渡して実行する。

## 型

| 型 | 説明 |
| --- | --- |
| `Int` | 64bit 符号付き整数。演算はラップアラウンド、`x / 0 == 0`、`x % 0 == x`（panic しない） |
| `Bool`, `String`, `()` | 文字列は不変 |
| `(A, B, ...)` | タプル（不変の値）。要素は `t.0`、`(A,)` は 1 要素 |
| `Option<T>`, `Result<T, E>` | `Some`/`None`, `Ok`/`Err` |
| `Vec<T>` | 伸長可能な配列（参照型） |
| `HashMap<K, V>`, `HashSet<T>` | core ライブラリのコレクション（下記） |
| `struct S<T> { f: T, mut g: T }` | フィールドは既定で不変、`mut` を付けたものだけ代入できる（参照型） |
| `enum E<T> { A, B(T, U) }` | 再帰的に定義してよい |
| `fn(A, B) -> R` | 関数・クロージャの値（純粋） |
| `Request`, `Response`, `TxError`, `NetError`, `IoError` | ホストが提供する不透明なデータ |
| `&Log`, `&Net`, `&Db`, `&Clock`, `&Random`, `&Fs`, `Tx`, `&Tx` | capability（下記） |

- struct・enum・`Vec`・`HashMap` は参照型で、代入や引数渡しは参照の共有になる。そのため capability 以外の `&T`・`&mut T` は `T` と同じ型として扱い、式の `&x`・`&mut x`・`*x` も何もしない（Rust の書き方をそのまま受け付けるため）。
- 型推論は関数本体の中だけで行う（関数のシグネチャは明示）。本体では `_` を型の代わりに書ける（`Vec<_>`）。

## generics

```kek
struct Pair<A, B> {
    first: A,
    second: B,
}

impl<A, B> Pair<A, B> {
    fn new(first: A, second: B) -> Self {
        Pair { first, second }
    }

    fn swap(self) -> Pair<B, A> {
        Pair::new(self.second, self.first)
    }
}

fn largest<T: Ord>(v: Vec<T>, d: T) -> T {
    v.iter().max().unwrap_or(d)
}
```

- 関数・struct・enum・`impl` が型パラメータを取れる。型引数は推論され、`f::<Int>(x)`、`Vec::<Int>::new()` のように明示もできる。
- `impl` ブロックがメソッドを定義する。レシーバは `self`・`&self`・`&mut self`（どれも同じ）。`Self` は `impl` の対象の型。`impl Pair<Int, Int>` のように特定の型引数だけに定義してもよい。
- 生成されるコードは型引数ごとに具体化される（単相化）。

## trait

```kek
trait Area {
    fn area(&self) -> Int;

    fn describe(&self) -> String {
        "area " + self.area().to_string()
    }
}

impl Area for Shape {
    fn area(&self) -> Int { ... }
}

fn total<T: Area>(v: Vec<T>) -> Int { ... }
fn show<T>(x: T) -> String where T: Area + Hash { ... }
```

- trait は必須メソッド（`;` で終わる宣言）と既定メソッドを持つ。スーパートレイト（`trait Ord: Eq + PartialOrd`）、関連型（`type Item;`、`Self::Item`、`T::Item`）、型パラメータ（`trait From<T>`）を書ける。
- 境界は `<T: A + B>`、`where T: A, Self::Item: Ord`、関連型の指定は `I: Iterator<Item = Int>`。
- 呼び出しはすべて静的に解決する（`dyn` はない）。メソッドは固有メソッド、組み込み、trait の順に探す。
- trait の関連関数は `T::default()`、`Default::default()`、`Point::default()` のように呼べる。
- 組み込み型（`Int`、`String`、`Vec` など）には、この program で定義した trait だけを実装できる（core の trait の実装は core にある）。
- `#[derive(PartialEq, Eq, PartialOrd, Ord, Hash, Default)]` を struct・enum に付けられる（`Default` は struct のみ）。`Hash` を導出できるのは `mut` フィールドのない struct だけ。

### 演算子と core の trait

| trait | 内容 |
| --- | --- |
| `PartialEq`, `Eq` | `==`・`!=`。`Int`・`Bool`・`String`・`()` は組み込みの比較 |
| `PartialOrd`, `Ord` | `<`・`<=`・`>`・`>=`（`Int` は組み込み）、`cmp -> Ordering`、`max`・`min` |
| `Hash`, `Hasher` | `x.hash(&mut h)`、`DefaultHasher::new()`、`h.finish()` |
| `Default` | `default() -> Self` |
| `Iterator`, `DoubleEndedIterator`, `IntoIterator`, `FromIterator<A>`, `Sum<A>`, `Product<A>` | 下記 |

core は `Int`・`Bool`・`String`・`()`・タプル（8 要素まで）・`Option`・`Result`・`Vec` にこれらを実装している。

## クロージャ

```kek
let k = 10;
let add_k = |x| x + k;
let f: fn(Int) -> Int = |x: Int| -> Int { x * 2 };
fn apply<F: Fn(Int) -> Int>(f: F, x: Int) -> Int { f(x) }
fn compose(f: fn(Int) -> Int, g: impl Fn(Int) -> Int) -> fn(Int) -> Int { move |x| g(f(x)) }
```

- クロージャは純粋な第一級の値で、変数・フィールド・`Vec` に入れられる。名前付きの関数も値として使える（`apply(double, 3)`）。
- 変数は値で捕捉する（`move` は書いても書かなくてもよい）。捕捉した変数への代入はできない（状態は struct のフィールドに置く）。
- capability を捕捉したり引数に取ったりはできない（クロージャは I/O をしない）。例外は `db.transaction(|tx| ...)` の本体で、これは第二級のまま。
- 型の書き方は `fn(A) -> R`、`impl Fn(A) -> R`、境界 `F: Fn(A) -> R`（`FnMut`・`FnOnce` も同じ）。

## capability

副作用は capability を通してしか起こせない。capability は**第二級**である。

- 関数の引数（`&Cap`）としてのみ受け取れる。変数への束縛、戻り値、構造体のフィールド、`Vec` の要素、クロージャの捕捉にはできない。
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

- `let x = e;`, `let mut x: T = e;`, `let (a, mut b) = e;`（パターンは反駁不能であること）, `x = e;`, `s.f = e;`（`mut` フィールドのみ）
- `if c { } else if d { } else { }`、`if let P = e { } else { }`（式）
- `match e { pat => e, ... }`（式、網羅性検査あり、ネスト可）
- `while c { }`, `while let P = e { }`, `for x in a..b { }`, `for (i, x) in iter { }`, `break;`, `continue;`, `return e;`
- `for` は範囲・`Vec`・`Iterator`・`IntoIterator`（`HashMap`・`HashSet` など）を回る
- `e?`：`Result` / `Option` の早期リターン（エラー型は一致が必要）
- 演算子：`+ - * / %`（`String` の `+` は連結）、`== != < <= > >=`（上記の trait）、`&& || !`
- 範囲 `a..b`・`a..=b` は core の `Range`・`RangeInclusive`（`Int` のイテレータ）
- パターン：`_`、変数（`mut x`）、整数・文字列・真偽値リテラル、タプル `(p, q)`、`Some(p)`、`None`、`Ok(p)`、`Err(p)`、`E::V(p, ...)`、`V`

## イテレータ

```kek
let squares: Vec<Int> = (1..6).map(|x| x * x).collect();
let total: Int = v.iter().filter(|x| x % 2 == 0).sum();
for (i, w) in words.iter().enumerate() { ... }
```

- `Iterator` は `type Item;` と `fn next(&mut self) -> Option<Self::Item>` を持つ。自分の型に実装すれば `for` やアダプタが使える。
- アダプタ：`map`, `filter`, `filter_map`, `enumerate`, `zip`, `chain`, `take`, `skip`, `take_while`, `skip_while`, `step_by`, `peekable`（`peek`）, `rev`（`DoubleEndedIterator`）
- 消費：`count`, `last`, `nth`, `fold`, `for_each`, `any`, `all`, `find`, `find_map`, `position`, `collect`（`Vec`・`String`・`HashMap`・`HashSet` へ）, `sum`, `product`, `max`, `min`, `max_by_key`, `min_by_key`
- `v.iter()` は `Vec` を添字で回る。`for x in v` と同じく、回っている間に `push` された要素も見える。

## コレクション

### `Vec<T>`

`Vec::new()`, `push`, `get(i) -> Option<T>`, `set(i, x) -> Bool`, `pop`, `len`, `iter`, `join(sep)`（`Vec<String>`）

`for x in v` は毎回 `v.len()` を読み直す。本体で `v`（やその別名）に `push` すると終わらないので注意する。

### `HashMap<K, V>` と `HashSet<T>`

キーは `Hash + Eq + Ord`。

- `HashMap`：`new`, `with_capacity`, `insert(k, v) -> Option<V>`（古い値）, `get(&k) -> Option<V>`, `get_or(&k, d)`, `contains_key`, `remove(&k) -> Option<V>`, `len`, `is_empty`, `clear`, `iter`（`(K, V)`）, `keys`, `values`, `retain`, `extend`
- `HashSet`：`new`, `insert(x) -> Bool`, `contains`, `remove -> Bool`, `len`, `is_empty`, `clear`, `iter`, `extend`, `retain`, `is_subset`, `union`・`intersection`・`difference`（`Vec<T>` を返す）
- 反復は**挿入順**（既存のキーへの `insert` は位置を保ち、`remove` の後の `insert` は末尾）。ハッシュは固定（seed なし）なので結果は決定的。
- 反復は**生きたビュー**で、反復中に追加されたエントリも見え、削除されたエントリは飛ばす。
- 同じバケットへの衝突が 8 を超えると、そのバケットはキーの順序で並べた木になる。わざと衝突させる入力（HashDoS）でも各操作は O(log n)。

## 組み込みメソッド（抜粋）

- `Int`：`to_string`, `abs`, `min`, `max`, `bit_and`, `bit_or`, `bit_xor`, `shl`, `shr`, `ushr`, `cmp`
- `String`：`len`, `char_at(i) -> Option<Int>`（UTF-16）, `slice(a, b)`, `index_of`, `contains`, `starts_with`, `ends_with`, `split`, `replace`, `trim`, `to_upper`, `to_lower`, `parse_int`, `to_bytes`；`String::from_char(c)`, `String::from_bytes(v)`
- `Option`/`Result`：`is_some`, `is_none`, `is_ok`, `is_err`, `unwrap_or`
- `Request`：`method`, `path`, `segment(i)`, `query(k)`, `header(k)`, `body`
- `Response::text(status, body)`, `json`, `empty`, `no_content`, `not_found`, `bad_request`, `.with_header(k, v)`

範囲外アクセスは `Option` で表され、panic は起きない。

文字列は UTF-16 のコード単位の列で、`len`・`char_at`・`slice`・`index_of` はコード単位で数える。

- `split("")` はコードポイントごとに分ける。
- `trim` は Unicode の空白と改行（U+0009–000D、U+0020、U+00A0、U+1680、U+2000–200A、U+2028、U+2029、U+202F、U+205F、U+3000、U+FEFF）を除く。
- `to_upper` / `to_lower` は ASCII だけを変換する。
- `to_bytes` は UTF-8 に符号化する（孤立したサロゲートは U+FFFD）。
- `from_bytes` は WHATWG の規則で復号する（不正な列は U+FFFD、先頭の BOM は除く）。
- `String` の順序（`<`、`cmp`）は UTF-16 のコード単位の辞書順。

## 実行モデル

- I/O に到達する関数は、コンパイラがステートマシンに変換する。async/await の色分けはない。
- それ以外の関数は普通の wasm 関数になる。generic な関数は型引数ごとに具体化され、trait のメソッド呼び出しは実装へ静的に振り分けられる。クロージャは「シグネチャごとの enum と apply 関数」に変換される（非関数化）。
- `#[handler]` のプログラムは Workers で動き、capability は Worker のバインディングから作られる（[runtime.md](runtime.md)）。
- `#[main]` のプログラムは WASI のコマンドになる（`kek run` は wasmtime で実行する）。
  - `&Fs`・`&Log`・`&Clock`・`&Random` は WASI で実装される。`&Log` の `info` は標準出力、`warn` と `error` は標準エラーに書く。
  - `&Db` はプロセス内のインメモリのストアで、outbox の内容は標準エラーに表示される。
  - `&Net` は使えず、常に `NetError` を返す。
- 意味論の基準は Lean で書いた IR の参照インタプリタ（`lean/`）で、コンパイラとは差分テストで突き合わせる。core ライブラリ（`HashMap` やイテレータ）も IR に含まれ、参照インタプリタで同じように実行される。
