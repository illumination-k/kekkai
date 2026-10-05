# Kekkai 言語リファレンス（現行実装）

設計の背景は [design.md](design.md)、generics・trait・コレクションの設計は [generics.md](generics.md)。ここではセルフホストのコンパイラ（`compiler/`、`./kek`）が現在受け付ける言語を説明する。構文と標準ライブラリの名前は Rust に合わせている。

## プログラムの構成

- 1 ファイル、または 1 ディレクトリ（中の `*.kek` すべてが 1 つの名前空間）が 1 プログラム。
- トップレベルは `struct`・`enum`・`trait`・`impl`・`fn`・`type`・`const` のみ。グローバル変数はない（＝暗黙の権限がない）。`const` は純粋な値で、使うたびに評価される（下記「定数」）。
- どのプログラムにも core ライブラリ（`lib/core`：比較・ハッシュ・`Default`・表示（`Display`・`Debug`）・演算子（`Add` など）・イテレータ・`Vec`／`Int`／`String`／`Option`／`Result` のメソッド・`HashMap`／`HashSet`・ラベル付きの値の `Labeled`・権限の `Can`・シリアライズの `Value`／`Json`／`Toml`・時刻の `Timestamp`／`Duration`／`Date`）が含まれる。core の型名と trait 名は予約されている。`__` で始まる名前は core と prelude だけが使える。
- エントリポイントは次のどちらか一つ。
  - `#[handler] fn h(req: Request, db: &Db, ...) -> Response`：Workers の HTTP ハンドラ（`#[handler(idempotent)]` は冪等なハンドラ。下記）
  - `#[main] fn main(args: Vec<String>, fs: &Fs, ...) -> Int`：コマンドラインプログラム（`kek run`）
- `#[test]` 関数は `kek test` がモック capability を渡して実行する。capability 以外の引数（`Int`・`String`・`Vec`・自前の struct/enum など）を取るとプロパティベーステストになり、引数は生成される（`#[test(cases = N)]` でケース数を指定）。テストの中でだけ `assert!`・`assert_eq!` などが使える（下記「マクロ」）。

## 型

| 型 | 説明 |
| --- | --- |
| `Int` | 64bit 符号付き整数。演算はラップアラウンド、`x / 0 == 0`、`x % 0 == x`（panic しない） |
| `Bool`, `String`, `()` | 文字列は不変 |
| `(A, B, ...)` | タプル（不変の値）。要素は `t.0`、`(A,)` は 1 要素 |
| `Option<T>`, `Result<T, E>` | `Some`/`None`, `Ok`/`Err` |
| `Vec<T>` | 伸長可能な配列（参照型） |
| `HashMap<K, V>`, `HashSet<T>` | core ライブラリのコレクション（下記） |
| `Labeled<L, T>` | ラベル付きの値（個人情報は `Labeled<PII, T>`）。文字列にできない（下記） |
| `Can<A, r>` | 資源 `r`（変数）への操作 `A` の権限（下記） |
| `struct S<T> { f: T, mut g: T }` | フィールドは既定で不変、`mut` を付けたものだけ代入できる（参照型） |
| `struct S<T>(T, mut U);`, `struct S;` | タプル構造体（フィールドは `s.0`）とユニット構造体（下記「構造体と列挙型の形」） |
| `enum E<T> { A, B(T, U), C { x: T, y: U } }` | 再帰的に定義してよい。`C { .. }` は構造体のようなバリアント |
| `fn(A, B) -> R` | 関数・クロージャの値（純粋） |
| `Request`, `Response`, `TxError`, `NetError`, `IoError` | ホストが提供する不透明なデータ |
| `&Log`, `&Net`, `&Db`, `&Clock`, `&Random`, `&Fs`, `Tx`, `&Tx` | capability（下記） |

- struct・enum・`Vec`・`HashMap` は参照型で、代入や引数渡しは参照の共有になる。capability 以外の `&T`・`&mut T` は型としては `T` と同じで、実行時には何もしないが、どの参照から書き換えられるかを表す（下記「可変性」）。式の `&x`・`&mut x` も同じで、`*x` は何もしない。
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
- `impl` ブロックがメソッドを定義する。レシーバは `self`・`mut self`・`&self`（読み取り専用）・`&mut self`（書き換える）。`Self` は `impl` の対象の型。`impl Pair<Int, Int>` のように特定の型引数だけに定義してもよい。
- 生成されるコードは型引数ごとに具体化される（単相化）。

## 定数

```kek
const LIMIT: Int = 10;
const PRIMES: Vec<Int> = primes_below(LIMIT);

impl Point {
    const ORIGIN: Point = Point { x: 0, y: 0 };

    fn dist(&self) -> Int { (self.x - Self::ORIGIN.x).abs() + (self.y - Point::ORIGIN.y).abs() }
}

let m = Int::MAX;   // core の関連定数（`Int::MIN` も）
```

- `const NAME: T = e;` をトップレベルに、関連定数を固有の `impl` ブロックに書ける（`Type::NAME`、`impl` の中では `Self::NAME`）。trait の関連定数と trait の実装の中の定数はまだない。
- 定数は引数のない純粋な関数として扱われ、**使うたびに評価される**（`Vec` の定数は使うたびに新しい値）。初期化式は capability を使えない（関数の引数がないので capability の変数が見えない：`const NOW: Int = clock.now_ms();` は ``cannot find value `clock` in this scope``）。初期化式から関数や他の定数を呼んでよい。
- 名前空間は関数と同じで、同じ名前の関数や定数はエラー。変数・引数・パターンの束縛に定数の名前は使えない（使う場所の名前はいつも定数を指す）。
- 整数リテラル（`-3` も）の値の定数はパターンに書ける（`match x { LIMIT => .. }` はその値と比べる）。ほかの定数はパターンにできない。
- 篩型の検査器は整数リテラルの値の定数を値として知っている（関数本体でも、`type Small = Int where self < LIMIT` のような述語の中でも）。
- `kek caps` は定数を `const NAME: T` と表示し、定義のハッシュ（`kek hash` など）では使った定数が依存になる。

## 構造体と列挙型の形

```kek
#[derive(PartialEq, Clone, Hash, Serialize, Deserialize)]
struct Meters(Int);            // タプル構造体
struct Pair<A, B>(A, mut B);   // `mut` を付けたフィールドだけ代入できる
struct Marker;                 // ユニット構造体

enum Shape {
    Rect { w: Int, h: Int },   // 構造体のようなバリアント
    Dot,
    Line(Int, Int),
}

let m = Meters(3);
let x = m.0 + Pair(1, 2).1;
let Meters(n) = m;
let k = Marker;
let r = Shape::Rect { h: 2, w: 1 };          // フィールドの順は自由。評価は書いた順
match r {
    Shape::Rect { w: 0, .. } => "flat",
    Shape::Rect { w, h: height } => ...,
    _ => ...,
}
let Point { x, mut y } = p;                   // 構造体のパターン（let・match・if let・while let・for）
```

- タプル構造体 `struct S(A, B);` のフィールドは `0`・`1`… という名前で、`s.0` で読み、`S(a, b)` で作り、`S(p, q)` でパターンにする。数が合わないとエラー。`impl` の中では `Self(a, b)` とも書ける。フィールドは既定で不変で、`struct S(mut Int);` のように型の前に `mut` を付けたものだけ `s.0 = e` で代入できる。
- ユニット構造体 `struct S;` はフィールドのない構造体で、値もパターンも `S`（`S {}` とも書ける）。
- 構造体のようなバリアント `V { a: A, b: B }` は `E::V { a: x, b: y }` で作る（すべてのフィールドが必要。`E::V { a, b }` の省略形も可）。順番どおりに書かなくてもよく、値は書いた順に評価される。位置で書くこと（`E::V(x, y)`）はできない。
- 構造体のパターン `S { a, b: p, mut c, .. }`・`E::V { a, .. }`：`a` は `a: a` の省略形、`..` は残りのフィールドを無視する。`..` がないときはすべてのフィールドを書く（書かないと ``pattern `S` does not mention field(s) `b` ``）。ない名前のフィールドはエラー。入れ子にでき、`match`・`if let`・`while let`・`let`・`for` のどれにも使える。網羅性の検査は構造体をひとつのコンストラクタとして扱う（``missing `Point { .. }` ``）。
- `#[derive(...)]` はどの形にも使える。シリアライズは serde と同じで、ユニット構造体は `null`、フィールドが 1 つのタプル構造体（newtype）はそのフィールドの値、2 つ以上は配列、構造体のようなバリアントは `{"V": {"a": x, "b": y}}`（[serde.md](serde.md)）。`kek test` のプロパティテストはどの形も生成し、Rust の Debug と同じ形（`Meters(3)`、`Rect { w: 1, h: 2 }`）で表示する。
- 実装：これらはコンパイラの中で既存の形に書き換えられる（`compiler/desugar.kek`）。タプル構造体はフィールド `0`・`1` の構造体、構造体のようなバリアントは位置のバリアント、定数は引数のない関数になる。タプル構造体の名前を関数の値として渡すこと（`v.iter().map(Meters)`）はまだできない。

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
- `#[derive(PartialEq, Eq, PartialOrd, Ord, Hash, Default, Clone, Debug, Serialize, Deserialize)]` を struct・enum に付けられる（`Default` は struct のみ）。`Debug` の出力は Rust と同じ（`Point { x: 1, y: 2 }`、`Some(3)`、`Circle(Point { .. }, 3)`、文字列は `"a\"b\n"` のように引用・エスケープ）。`Hash` を導出できるのは `mut` フィールドのない struct だけ。`Labeled` を含む型（フィールドの型に `Labeled` が現れる）に導出できるのは `Clone` と `Deserialize` だけ。

### 演算子と core の trait

| trait | 内容 |
| --- | --- |
| `PartialEq`, `Eq` | `==`・`!=`。`Int`・`Bool`・`String`・`()` は組み込みの比較 |
| `PartialOrd`, `Ord` | `<`・`<=`・`>`・`>=`（`Int` は組み込み）、`cmp -> Ordering`、`max`・`min` |
| `Hash`, `Hasher` | `x.hash(&mut h)`、`DefaultHasher::new()`、`h.finish()` |
| `Default` | `default() -> Self` |
| `Clone` | `clone(&self) -> Self`：所有する深い複製（下記「可変性」） |
| `Display`, `Debug` | `fmt(&self, f: &mut Formatter)`：`{}`・`to_string()` と `{:?}`（下記「表示」） |
| `Add<Rhs>`, `Sub<Rhs>`, `Mul<Rhs>`, `Div<Rhs>`, `Rem<Rhs>`, `Neg` | `+ - * / %` と単項 `-`（下記「演算子のオーバーロード」） |
| `Iterator`, `DoubleEndedIterator`, `IntoIterator`, `FromIterator<A>`, `Sum<A>`, `Product<A>` | 下記 |

core は `Int`・`Bool`・`String`・`()`・タプル（8 要素まで）・`Option`・`Result`・`Vec` にこれらを実装している（`Clone` は `HashMap`・`HashSet`・`Labeled` にも。`Display` は `Int`・`Bool`・`String`・`()` と時刻の型だけ、`Debug` は `HashMap`・`HashSet`・`Ordering`・時刻の型にも）。

### 表示（`Display`・`Debug`）

```kek
trait Display {
    fn fmt(&self, f: &mut Formatter);
    fn to_string(&self) -> String { ... }   // 既定メソッド
}
trait Debug {
    fn fmt(&self, f: &mut Formatter);
}

impl Display for Point {
    fn fmt(&self, f: &mut Formatter) {
        write!(f, "({}, {})", self.x, self.y)
    }
}
```

- `Formatter` は文字列を組み立てるだけの core の struct で、`f.write_str(s)`、`f.write_display(&x)`、`f.write_debug(&x)` と `write!(f, ...)`・`writeln!(f, ...)` で書く。Rust と違い `fmt` は何も返さない（書き込みは失敗しない）ので、`fmt::Result` も `?` も要らない。
- `x.to_string()` は `Display` を実装したどの型にも使える（`Display` の既定メソッド。`Int`・`Bool` は組み込み、`Duration`・`Date` は固有メソッドが優先）。
- `Labeled` はどちらも実装しない。`format!("{}", u.email)`・`format!("{:?}", u.email)`・`u.email.to_string()` は型エラーで、`"x" + l` と同じく `map`・`zip`・`and_then` と格下げの方法が添えられる。

### 演算子のオーバーロード

```kek
impl Add for V2 {                 // `impl Add<V2> for V2` と同じ
    type Output = V2;
    fn add(self, rhs: V2) -> V2 { V2 { x: self.x + rhs.x, y: self.y + rhs.y } }
}
impl Mul<Int> for V2 { type Output = V2; fn mul(self, k: Int) -> V2 { ... } }

fn sum_all<T: Add<Output = T>>(xs: Vec<T>, zero: T) -> T { ... acc = acc + x; ... }
```

- `Int`（`+` では `String` も）以外の値の `a + b` は `Add::add(a, b)` の呼び出しになる（`-`：`Sub`、`*`：`Mul`、`/`：`Div`、`%`：`Rem`、単項 `-`：`Neg`）。右辺の型は trait の引数で、省くと `Self`（Rust の `Rhs = Self`）。結果の型は `Output`。
- 呼び出しは静的に解決し、`kek assure`・`kek affected` などの依存（defhash）には実装が入る。篩型の検査器は整数の演算としては扱わない。
- core は `Int`（全部）と `String`（`Add`）に実装しているので、generic な関数から使える。時刻の型には `Duration + Duration`、`Duration - Duration`、`-Duration`、`Duration * Int`、`Timestamp + Duration`、`Timestamp - Duration` がある（2 つの `Timestamp` の差は `t.since(&earlier)`）。
- 同じ型に同じ trait を右辺の型ごとに複数実装できる（`Mul<Int>` と `Mul<V2>`）。ただし実装の中では `Self::Output` ではなく具体的な型を書く。

## クロージャ

```kek
let k = 10;
let add_k = |x| x + k;
let f: fn(Int) -> Int = |x: Int| -> Int { x * 2 };
fn apply<F: Fn(Int) -> Int>(f: F, x: Int) -> Int { f(x) }
fn compose(f: fn(Int) -> Int, g: impl Fn(Int) -> Int) -> fn(Int) -> Int { move |x| g(f(x)) }
```

- クロージャは純粋な第一級の値で、変数・フィールド・`Vec` に入れられる。名前付きの関数も値として使える（`apply(double, 3)`）。
- 変数は値で捕捉する（`move` は書いても書かなくてもよい）。捕捉した変数への代入はできない（状態は struct のフィールドに置く）。捕捉した値は束縛の可変性と view を保つ（`let mut v` を捕捉すれば `v.push(..)` できる。`&T` を捕捉したクロージャは読み取り専用の値）。
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
| `&Fs` | `read(path)`, `write(path, s)`, `write_bytes(path, Vec<Int>)`, `list(dir)` → `Result<_, IoError>`；標準入出力は `read_line() -> Option<String>`、`read_stdin(n) -> Option<String>`（n バイト）、`write_stdout(s)`（改行なし） |
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

### 冪等なハンドラ（`#[handler(idempotent)]`）

```kek
#[handler(idempotent)]
fn handle(req: Request, db: &Db, log: &Log) -> Response { ... }
```

- 冪等なハンドラと、そこから呼び出しグラフで到達できる関数は、冪等な capability の操作しか使えない。リトライ（同じリクエストの再送、Workers の再実行）で状態が変わらないことを型検査で保証する。
- 冪等とみなす操作：`log.*`、`clock.now_ms`、`net.get`、`db.get`、`db.transaction`、`tx.get`、`tx.put`（同じキーの上書き）、`tx.delete`（2 回目は何もしない）、`tx.commit`・`tx.rollback`、`fs.read`・`fs.list`・`fs.set_cwd`。
- それ以外は冪等でない：`net.post`、`tx.outbox`、`fs.write`・`fs.write_bytes`、`fs.read_line`・`fs.read_stdin`（入力を消費する）、`fs.write_stdout`、`random.int`（リトライで別の ID などを作ってしまう）。新しい操作は一覧に加えるまで冪等でないとみなす。
- 違反はハンドラに報告する：``idempotent handler `h` reaches `net.post` via `settle` -> `charge` (payments.kek:12), which is not idempotent: a retry would do it again``。
- 冪等性は操作の種類で判定する。`tx.put` に乱数や時刻を書く・キーの有無で分岐して別の効果を起こす、といった値に依存する性質は見ない。Idempotency-Key で重複を検出する `tx.outbox` のような、実装上冪等なパターンも型では冪等と認めない（`#[handler]` のまま使う）。
- `kek caps` は冪等なハンドラに `#[handler(idempotent)]`、capability を受け取る関数に `idempotent: true|false` を表示する（JSON は全関数の `idempotent`）。`kek assure` は `idempotent` の保証を記録する。
- 実行時の動作は変えない。Workers 側のリトライの設定（Queues・Workflows の再試行など）に使う場合は、`kek caps -json` の `idempotent` を参照する（ビルド出力にはまだ含めない）。

## ラベル付きの値（`Labeled<L, T>`）

```kek
struct User {
    id: Int,
    email: Labeled<PII, String>,
}

fn greet(log: &Log, u: User) {
    log.info("hello " + u.email.mask());       // "a****@example.com"
    log.info("user " + u.email.hash());        // 16 桁の16進数
    // log.info(u.email);                      // 型エラー
}

fn same_domain(a: User, b: User) -> Labeled<PII, Bool> {
    let da = a.email.map(|e| e.split("@").get(1).unwrap_or(""));
    da.zip(&b.email, |d, e| e.ends_with("@" + d))
}
```

`Labeled<L, T>` は core の generic な struct（`lib/core/labeled.kek`）で、ラベル `L` の付いた値を包む。`L` は型で、core に個人情報の `PII` がある（利用者は `struct Secret {}` のような任意の struct をラベルにできる）。フィールド `__value` は core の外から書けない名前なので、読むことも `Labeled { ... }` で作ることもできない。設計と実装の詳細は [authz-flow.md](authz-flow.md)。

| 操作 | 内容 |
| --- | --- |
| `PII::label(x)`、`Labeled::new(x)` | 包む（`new` のラベルは期待される型から） |
| `l.map(|x| ...)` | 中身（`&T`）を計算する。結果は同じラベルの `Labeled<L, U>` |
| `l.zip(&m, |x, y| ...)` | 同じラベルの 2 つを組み合わせる（比較もここで：`a.zip(&b, |x, y| x == y)` は `Labeled<L, Bool>`） |
| `l.and_then(|x| ...)` | 中身から `Labeled<L, U>` を作る |
| `l.mask() -> String` | `Labeled<L, String>` のみ。先頭の 1 文字と最後の `@` 以降を残し、ほかの文字（コードポイント）を `*` にする：`"alice@example.com"` → `"a****@example.com"`、`"bob"` → `"b**"`。`@` の前が 1 文字ならそれも隠す（`"a@x.com"` → `"*@x.com"`、`"x"` → `"*"`）。`""` は `""` |
| `l.hash() -> String` | `T: Hash`。core の固定（seed なし）のハッシュ（`DefaultHasher`）を 16 桁の小文字の16進数で。等しい値は等しいハッシュになるので、突き合わせや集計のキーに使える |
| `l.expose_unchecked() -> T` | 値そのもの（脱出口） |
| `clone()` | `T: Clone` なら |

- `Labeled` は表示（`Display`・`Debug`）・変換・比較・順序・ハッシュ・演算子の trait を実装しない。`log.info(l)`・`"x" + l`・`format!("{}", l)`・`Response::text(200, l)`・`tx.put(k, l)`・`l.to_string()`・`l == m`・`if` の条件はどれも型エラーになり、エラーには `map`・`zip`・`and_then` と格下げの方法が添えられる。trait の境界を満たさないので generic な関数経由でも漏れない。違うラベルの値は `zip` で組み合わせられない。
- **暗黙のフロー**：ラベル付きの値で分岐できるのは `map`・`zip`・`and_then` に渡すクロージャの中だけで、その結果は同じラベルで包まれる。クロージャは中身を読み取り専用で受け取り、書き換えられる状態（`&T` で借りていない `Vec` など）と関数の値を捕捉できない（capability はもともと捕捉できない）。関数を渡すときはクロージャ式か名前付きの関数（可変な状態に届く引数は `&T`）。違反は ``the closure given to `map` cannot capture `seen: Vec<Int>`: it has mutable state that the closure could write the labeled value to`` のように報告する（`kek check -json` の phase `flow`）。
- そのため、格下げしないプログラムでは素の出力はラベル付きの入力に依存しない（非干渉性。終了と時間のチャネルは除く。コア計算での証明は `lean/Kekkai/Flow.lean`）。
- 格下げ（`mask`・`hash`・`expose_unchecked` の呼び出し）は `kek caps` に関数ごとに一覧され（`declassify`）、`kek assure` に前提 `flow.declassify` として記録される。関数に `#[declassify(reason = "...", owner = "...", expires = "YYYY-MM-DD")]` を付けると、その関数の格下げの承認者・理由・期限になる（`kekkai.toml` の `[flow] declassify_requires` で必須にでき、`[flow] max_declassify_per_module` でファイルごとの数を制限できる。[assure.md](assure.md)）。
- `#[derive]` は `Labeled` を含む型に `Clone` だけを導出できる。`kek test` は `Labeled` の値を生成しない（ライブラリの型）。
- 以前の `Pii<T>` は `Labeled<PII, T>` になった。`kek fix` が `Pii<T>` と `Pii::new(x)` を書き換える（`Pii` どうしの `==` は `zip` に手で直す）。

## 認可（`Can<A, r>`）

```kek
struct Edit {}

#[policy]
fn can_edit(u: User, d: Doc) -> Option<Can<Edit, d>> {
    if d.owner == u.id { Some(Can::grant()) } else { None }
}

fn rename(mut d: Doc, t: String, _cap: Can<Edit, d>) {
    d.title = t;
}

fn handle(u: User, doc: Doc, other: Doc) {
    match can_edit(u, doc) {
        Some(cap) => rename(doc, "new", cap),
        // rename(other, "new", cap) は型エラー：the permission is for `doc`, not `other`
        None => {}
    }
}
```

- `Can<A, r>` は「資源 `r` に操作 `A` をしてよい」という権限（`lib/core/can.kek`）。`A` は型（`struct Edit {}`）、`r` は**変数**：シグネチャではその関数の引数（`self` も可）、本体の型注釈ではスコープにある変数。資源は型の一部で、`doc` の権限を `other` に使うと型エラーになる（同名の別の変数も区別する）。
- 呼び出しでは、シグネチャの資源が実引数で置き換わる。`can_edit(u, doc)` の結果は `Option<Can<Edit, doc>>`。資源として名指される位置の実引数は変数でなければならない（式なら `let` で束縛する）。`let mut` の変数は資源にできない。
- 権限を作れるのは `#[policy]` を付けた関数の `Can::grant()` だけ（`#[policy(reason = "...", owner = "...", expires = "...")]` も可）。チェックを通らずに権限を要る関数へ到達する経路はない。ポリシー自体の正しさは保証しない。
- 権限は引数・戻り値・局所変数・`Option` などに置けるが、struct・enum のフィールドには書けない（資源を名指せない）。局所変数の権限は関数の外に出ない。資源を名指す関数は値として使えない。
- `kek caps` は `#[policy]`、`requires: Edit(d)`、`grants: Edit(d)` を表示し、`kek assure` はポリシーを前提 `policy`、受け取る権限を保証 `authz.requires.Edit(d)` として記録する。
- 実行時には中身のない値で、資源は表現を持たない。

## シリアライズ（`Serialize`・`Deserialize`）

```kek
#[derive(Serialize, Deserialize)]
struct Server {
    name: String,
    port: Port,               // type Port = Int where 0 < self && self < 65536
    backup: Option<Port>,
}

let s: Server = Json::from_str(text)?;     // Toml::from_str も同じ
Json::to_string(&s)
```

- 型は共通のデータモデル `Value` と変換し（`to_value`・`from_value`）、形式（`Json`・`Toml`）は `Value` と文字列を変換する。エラーは `SerdeError`（`message()` は `servers[1].port: the value is not a valid `Port`` のように場所と形だけを示し、データを含まない）。
- `Labeled<L, T>` は読めるが書けない（`Deserialize` だけを実装する）。`Can` と capability はどちらも実装しない。
- derive した `Deserialize` は篩型の別名の述語を調べる（`Option`・`Vec` の要素も）。
- 詳細は [serde.md](serde.md)。

## 時刻（`Timestamp`・`Duration`・`Date`）

```kek
let expires = Timestamp::now(clock).add(Duration::days(7));   // 時計を読むには &Clock
expires.to_rfc3339()                                         // "2026-10-12T09:30:00Z"
let d = Date::parse("2024-02-29")?;                          // 存在する日付だけ
```

- 計算は core の純粋な関数で、時計を読むのは `Timestamp::now(clock)` だけ。UTC と固定オフセットのみ（タイムゾーン名・夏時間はない）。
- `Serialize`・`Deserialize` を持つ（RFC 3339、`YYYY-MM-DD`、`"1h30m"`）。詳細は [time.md](time.md)。

## 篩型（refinement types）

```kek
type Port = Int where 0 < self && self < 65536;
type Index = Int where 0 <= self;

fn get(v: Vec<Int>, i: Int) -> Int
where
    0 <= i,
    i < v.len(),
{
    v[i]
}

// 事後条件：`result` は戻り値
fn clamp(x: Int, lo: Int, hi: Int) -> Int
where
    lo <= hi,
    lo <= result,
    result <= hi,
{
    if x < lo { lo } else if x > hi { hi } else { x }
}

fn sum(v: Vec<Int>) -> Int {
    let mut t = 0;
    for i in 0..v.len() {
        t = t + v[i]; // 0 <= i < v.len() は for の範囲から
    }
    t
}
```

- `where` 節には trait の境界（`T: Ord`）と述語を混ぜて書ける。述語は線形整数算術：整数リテラル、`Int` の引数、`v.len()`（`Vec`・`String`）、不変なフィールド（`p.x`）、`+`・`-`、定数との `*`、比較、`&&`・`||`・`!`。`result` を含む述語は事後条件、ほかは事前条件。
- `type Name = T;`・`type Name = T where pred;` は型の別名。透過的（`Port` は `Int` として使える）で、述語は値を `self` と書く。`Int` から `Port` への変換（引数・戻り値・型注釈付きの `let`・struct のフィールド）で述語の証明が要り、`Port` の値からは述語が事実として使える。
- `v[i]`（`Vec` のみ）は要素を直接返す。`0 <= i && i < v.len()` が証明できなければ型エラー（`get(i)` は `Option` を返すまま）。
- 検査器（`compiler/refine.kek`）は関数の本体を歩き、条件（`if`・`while`・`match` の整数パターン・`&&`/`||`）、`for` の範囲、不変な `let`、呼び出し先の事後条件、ループで増えるだけ／減るだけの変数、ループで保たれる `x <= y` を事実として、QF\_LIA のソルバ（`compiler/smt.kek`）で検証条件を示す。`v.len()` の事実は `push`・`pop` や `Vec` に届く呼び出しの後で忘れる。
- 証明できないとき、エラーには反例が付く：`cannot prove the index is in bounds: ``0 <= i && i < v.len()`` (counterexample: i = 0, v.len() = 0)`。`kek check -v`・`-json` は使った事実も示す。
- `/`・`%` の除数が 0 でない（`/` は `MIN / -1` でもない）ことと、`+ - *` がオーバーフローしないことは lint（警告）。既定では篩型を使う関数だけで検査し、`kekkai.toml` で変えられる：

```toml
[refine]
overflow = "error"   # "auto"（既定）| "off" | "lint" | "error"
division = "lint"
```

- 実行時の整数はラップアラウンドのまま（`x / 0 == 0`）。詳細と未実装のものは [refinement.md](refinement.md)。

## 可変性

参照で共有される値を、どの参照から書き換えられるかを Rust と同じ書き方で追跡する（設計と実装の決定は [mutability.md](mutability.md)）。所有権と move はないので、保証は「この参照からは書き換えられない」まで。

```kek
#[derive(Clone)]
struct Item {
    mut count: Int,
}

fn add(v: &mut Vec<Int>, x: Int) {
    v.push(x);
}

fn total(v: &Vec<Item>) -> Int {
    let mut n = 0;
    for it in v.iter() {
        n = n + it.count;   // 読むのはよい
        // it.count = 0;    // エラー：`&Vec<Item>` からたどった値は書き換えられない
    }
    n
}

fn reset_copy(v: &Vec<Item>) -> Vec<Item> {
    let mut copy = v.clone(); // 借用した値を所有するには clone()
    for mut it in copy.iter() {
        it.count = 0;
    }
    copy
}

fn main2() -> Int {
    let mut v = Vec::new();   // 書き換えるので `mut`
    add(&mut v, 1);
    v.len()
}
```

- **書き換え**（`push`・`set`・`pop`・`insert`・`remove`・`clear`・`retain`・`extend` などの変更する操作、`&mut self` のメソッド、`mut` フィールドへの代入）には、可変な経路が要る：`let mut x`、`mut x: T`・`&mut T` の引数、`mut self`・`&mut self`、パターンの `mut x`（`Some(mut s)`・`for mut x in v`・`|mut x|`）、一時値、それらのフィールド。`let x` の束縛からは書き換えられない（`let mut y = x;` で移せば書き換えられる）。
- **共有の参照**（`&T` の引数、`&self`、式 `&e`、`-> &T` の結果）からたどった値（フィールド・要素・イテレータの要素・パターンで束縛した値）は深く読み取り専用で、書き換えられず、所有する場所（struct のフィールド、コレクション、`let mut`、所有型の戻り値・引数）に置けない。`x.clone()` で所有する複製を作るか、受け取る側を `&T` にする。タプルや `Some(x)` で包んでも同じ。
- `Vec<&T>`・`Option<&T>` のように中に `&` を含む型は「入れ物は新しいが中身は共有」を表す：`let mut v: Vec<&Item> = items.iter().collect();` には要素を足せるが、要素は書き換えられない。`-> Option<&T>` で `v.get(i)` を返せる。
- 書き換えられる状態に届かない型（`Int`・`String`・不変なフィールドだけの struct・`Option<Int>` など）は対象外で、共有の参照から読んだ値も自由に使える。型パラメータの値は書き換えられる値として扱う。
- 引数：`x: T` と `mut x: T` は共有の値を受け取れない（呼び出し側の束縛は可変でなくてよい）。`x: &T` は何でも受け取る。`x: &mut T` には可変な経路の値か一時値を渡す（`&mut v` と書いても `v` と書いてもよい。`&v` はエラー）。
- イテレータ：共有のコレクションの `iter()` はイテレータ自体（カーソル）は新しい値なので `next` やアダプタを呼べ、要素は共有になる。`collect` した結果は中身が共有のコレクション。
- trait の実装のメソッドは、trait の宣言が `&self`・`&T` で受け取るものを `&self`・`&T` で受け取る。
- core の関数の結果は、共有の引数を受け取ると共有になる（`#[fresh]` を付けた関数は「中身が共有」になる。`#[fresh]` は core と prelude でだけ書ける）。
- 診断の例：

```
x.kek:3:5: cannot mutate `v`: it is not declared as mutable (write `let mut v`)
x.kek:7:9: cannot mutate `xs` through a shared reference `&Vec<Int>` (take `&mut Vec<Int>`)
x.kek:9:14: cannot store a borrowed value in `out`: it comes from the shared reference `item` (use `item.clone()`)
x.kek:12:5: cannot return a borrowed value as `Vec<Int>`: it comes from `&self` (return `&Vec<Int>` or clone it)
```

### `Clone`

`trait Clone { fn clone(&self) -> Self; }`（core）。`clone()` の結果は所有する深い複製で、共有の参照から得た値もこれで書き換えたり保存したりできる。`#[derive(Clone)]` は全フィールド（enum はペイロード）を複製する。core は `Int`・`Bool`・`String`・`()`・タプル・`Option`・`Result`・`Vec`・`HashMap`・`HashSet`・`Pii`（`T: Clone` のとき）に実装している。

### `kek fix`

`kek fix [-w] <paths>` は、書き換えに足りない `mut` を足す：`let mut`、`mut x: T`、`mut self`、`Some(mut x)`・`for mut x`・`|mut x|`、書き換えている `&T` の引数を `&mut T`（`&self` を `&mut self`）、`&mut T` に渡した `&v` を `&mut v`。直すと検査し直し、変わらなくなるまで繰り返す。編集はテキストへの挿入なのでコメントは保たれる。`-w` なしでは差分を表示する。trait のシグネチャは変えず、直せないエラー（借用した値の保存や返却）は一覧にして終了コード 1 で終わる（`clone()` か `&T` で手で直す）。各パスは 1 つのプログラム（`kek check` と同じ）。

## 文と式

- `let x = e;`, `let mut x: T = e;`, `let (a, mut b) = e;`（パターンは反駁不能であること）, `x = e;`, `s.f = e;`（`mut` フィールドのみ。`s` が可変であること）
- `let P = e else { ... };`（let-else）：`e` が反駁可能なパターン `P` に合えばその変数を束縛し、合わなければ `else` のブロックを実行する。ブロックは `return`・`break`・`continue`（またはそれで終わる `if`/`match`、`loop`）で終わらなければならない（構文として検査する）。型注釈は書けない。`let Some((a, mut b)) = o else { return 0; };`
- 複合代入 `x += e;`（`-= *= /= %= &= |= ^= <<= >>=` も）は `x = x + e;` と同じ。`s.f += e;`、`v[i] += e;` も書ける。左辺は 2 回評価されるので、変数・フィールド・要素（添字は呼び出しを含まない式。`v.len()` は可）に限る
- 要素の代入 `v[i] = e;`（`Vec` のみ）：読み出しの `v[i]` と同じく `0 <= i && i < v.len()` の証明が要り（[refinement.md](refinement.md)）、`v` は可変な経路であること（`push` と同じ）。core の `Vec::__index_set` の呼び出しになる
- `if c { } else if d { } else { }`、`if let P = e { } else { }`（式）
- `match e { pat => e, pat if guard => e, ... }`（式、網羅性検査あり、ネスト可）。ガード `if guard`（`Bool`）は腕の束縛を見られ、ガードのある腕は網羅性に数えない（Rust と同じ）
- `while c { }`, `while let P = e { }`, `loop { }`, `for x in a..b { }`, `for mut x in v { }`, `for (i, x) in iter { }`, `break;`, `continue;`, `return e;`
- `loop { }` は無限ループ（文）。`break` か `return` で抜ける。`break` のない `loop` は発散するので、`fn f() -> Int { loop { if c { return 1; } } }` は型が合う。値を返す `break e`・ラベル（`'a: loop`）は未対応
- `for` は範囲・`Vec`・`Iterator`・`IntoIterator`（`HashMap`・`HashSet` など）を回る
- `e?`：`Result` / `Option` の早期リターン（エラー型は一致が必要）
- 演算子：`+ - * / %`（`String` の `+` は連結）、`== != < <= > >=`（上記の trait）、`&& || !`、`Int` のビット演算 `& | ^ << >>` と `!x`（ビット反転）。`>>` は算術シフト、シフト量は 64 の剰余（組み込みメソッド `bit_and`・`shl` などと同じ）
- 優先順位は Rust と同じ：単項 `- ! & *` > `* / %` > `+ -` > `<< >>` > `&` > `^` > `|` > 比較 > `&&` > `||`（比較は連鎖できない）。`kek fmt` は比較やほかのビット演算の中のビット演算、シフトの中の算術に括弧を付ける（`(a & b) == 0`、`1 << (n - 1)`）
- 整数リテラル：`255`、`0xff`、`0o17`、`0b1010`、区切り `1_000_000`。`i64` の範囲を超えるとエラー。文字リテラル `'a'`、`'\n'`（`\r \t \\ \' \" \0 \x7f \u{1F600}`）はその Unicode のコードポイントの `Int`（文字の型はない）、バイトリテラル `b'a'`・`b'\xff'` はそのバイトの `Int`。パターンにも書ける。`kek fmt` は書いた綴りを保つ
- 範囲 `a..b`・`a..=b` は core の `Range`・`RangeInclusive`（`Int` のイテレータ）
- パターン：`_`、変数（`mut x`）、整数・文字・文字列・真偽値リテラル、整数の定数、タプル `(p, q)`、`Some(p)`、`None`、`Ok(p)`、`Err(p)`、`E::V(p, ...)`、`V`、構造体 `S { a, b: p, .. }`・`E::V { a, .. }`、タプル構造体 `S(p, q)`、ユニット構造体 `S`、or パターン `p | q`、整数の範囲 `lo..=hi`・`lo..hi`・`..=hi`・`lo..`、束縛 `x @ p`（`mut x @ p`）
  - or パターンはネストでき（`Some(1 | 2)`）、`match` の腕・`if let`・`while let` の先頭には `|` を書いてもよい。どの選択肢も同じ名前を同じ型・同じ可変性で束縛すること。網羅性検査は選択肢ごとに展開して数える
  - 範囲は `Int` だけで、`lo > hi`（`lo..hi` では `lo >= hi`）はエラー。`Int` は範囲を並べても網羅とみなさない（`_` が要る）
  - `x @ p` は p に一致した値全体を x に束縛する。p が or パターンなら括弧が要る（`x @ (A | B)`）
  - 篩型の検査は、整数の範囲・or パターン・`@` の束縛を腕（と `if let`）の事実にし、ガードのない腕の否定を後の `_` の腕の事実にする。ガードは腕の中だけの事実になる

## マクロ

```kek
let v = vec![1, 2, 3];
let grid = vec![vec![0; w]; h];          // 要素は clone される
log.info(format!("{name}: {} items, first = {:?}", v.len(), v.get(0)));
let s = format!("[{:>8}] [{:<5}] [{:^7}] [{:05}] [{:#x}] [{:+}] [{:.3}]", title, n, c, n, n, n, s);
if matches!(r, Ok(_)) { ... }

#[test]
fn parses() {
    assert!(parse("1").is_ok());
    assert_eq!(parse("1"), Ok(1), "input {}", "1");
}
```

マクロ `name!(...)`・`name![...]` は構文解析で普通の式に展開される（`compiler/macro.kek`）。以降の検査・コード生成は展開結果を見て、`kek fmt` は書いたとおりの呼び出しを出力する。式の位置にも文の位置にも書ける。自分でマクロを定義することはできず、知らない名前はエラーになる。

| マクロ | 展開 |
| --- | --- |
| `vec![a, b]`, `vec![]`, `vec![x; n]` | `Vec` を作って `push`。`vec![x; n]` は `x.clone()` を n 個（`T: Clone`） |
| `format!("...", args)` | `String`（下記） |
| `write!(f, "...", args)`, `writeln!` | `f.write_str(format!(...))`（`writeln!` は改行を足す）。`Display`・`Debug` の実装で使う |
| `matches!(e, pat)` | `match e { pat => true, _ => false }`（ガード `if` はまだない） |
| `assert!(c)`, `assert!(c, "...", args)` | 失敗すると `assertion failed: <c のソース>` かメッセージでテストを止める |
| `assert_eq!(a, b)`, `assert_ne!(a, b)`（後ろにメッセージも可） | `PartialEq` で比べ、両辺を `Debug` で表示する（`left: ..`・`right: ..`） |
| `panic!("...", args)`, `unreachable!()`, `todo!()`, `unimplemented!()` | テストを止める（型は何にでもなる） |

書式文字列はコンパイル時に解析する。`{}`（`Display`）、`{:?}`（`Debug`）、`{0}`（位置）、`{name}`（スコープの変数を捕捉、または名前付き引数 `name = e`）、`{{`・`}}`（波括弧そのもの）。書式指定は `{:[[fill]align][+][#][0][width][.precision][type]}`：

- `width` と `align`（`<` 左、`^` 中央、`>` 右）・`fill`（任意の 1 文字）は表示した文字列（コードポイント数）を詰める。揃えの既定は数（`Int`）が右、ほかは左。
- `+`（正でも符号）、`0`（符号と接頭辞の後ろを 0 で埋める）、`#`（`0x`・`0b`・`0o`）、`type` の `x`・`X`・`b`・`o`（16・2・8 進。負の数は Rust の i64 と同じく 2 の補数）は `Int` だけ。
- `.precision` は `String` だけで、先頭の n 文字に切る。
- `{:#?}`（整形した Debug）、`{:e}`、`width$` の引数指定は未対応。
- 誤りはコンパイルエラーになる：引数が足りない（`2 positional arguments in format string, but there is 1 argument`）、使われない引数（`argument never used`）、範囲外の位置、閉じていない `{`・対応のない `}`、知らない書式、文字列リテラルでない書式文字列、`Display`・`Debug` を実装しない値（`#[derive(Debug)]` や `impl Display` を勧める）。
- 引数はちょうど 1 回、書いた順に評価される。

`assert!` などの止まるマクロは **`#[test]` 関数の中でだけ**使える。Kekkai の本番コードは panic しない約束なので、ほかの場所（テストから呼ぶ補助関数も含む）では `` `assert!` can only be used in `#[test]` functions: Kekkai code does not panic; return a `Result` or an `Option` ... `` というエラーになる。テストで失敗すると、そのテストだけが止まり、メッセージが報告される（ほかのテストは続く）：

```
test eq_fails ... FAILED (panicked)
    panicked at asserts.kek:26:5:
    assertion `left == right` failed
      left: Pair { a: 4, b: "x" }
     right: Pair { a: 5, b: "x\n" }
```

実行時には prelude の `__panic_report` が標準エラーにこれを書いて終了コード 101 で終わる（モックの `log` の行と outbox も続けて出す）。prelude のない IR（`kek ir`、Lean の参照インタプリタ）では `unreachable` になる。`println!`・`print!`・`eprintln!`・`dbg!` は使えない（出力には capability が要る）。エラーは `log.info(format!(...))` を勧める。

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

組み込み：`Vec::new()`, `push`, `get(i) -> Option<T>`, `set(i, x) -> Bool`, `pop`, `len`, `join(sep)`（`Vec<String>`）。`v[i]` で読み、`v[i] = x` で書く（範囲の証明が要る）。core（`lib/core/vec.kek`・`iter.kek`）は Rust の `Vec`・スライスと同じ名前のメソッドを足している。

- 生成・情報：`Vec::with_capacity(n)`（容量は持たないので `new` と同じ）, `iter`, `is_empty`, `first`, `last`, `slice(a, b)`（`v[a..b].to_vec()` に当たる新しい `Vec`）
- 書き換え（`&mut self`）：`insert(i, x)`, `remove(i) -> Option<T>`, `swap_remove(i) -> Option<T>`, `swap(i, j)`, `reverse`, `truncate(n)`, `clear`, `extend(iter)`（`Iterator`。`Vec` は `v.iter()` を渡す）, `append(&mut other)`（`other` は空になる）, `split_off(at) -> Vec<T>`, `retain(|x| ..)`, `dedup_by(|a, b| ..)`, `dedup_by_key(|x| ..)`, `rotate_left(k)`, `rotate_right(k)`
- 整列：`sort_by(|a, b| Ordering)`, `sort_by_key(|x| k)` は安定なマージソート（O(n log n)）。`sort_unstable_by`・`sort_unstable_by_key` は同じもの。`binary_search_by(|x| Ordering) -> Result<Int, Int>`、`binary_search_by_key(&k, |x| ..)`、`partition_point(|x| ..)`
- 分割：`chunks(n)`, `windows(n)`（`Vec<Vec<T>>`。`n < 1` は空）
- `T: PartialEq`：`contains(&x)`, `starts_with(&v)`, `ends_with(&v)`, `dedup`（連続する重複を除く）
- `T: Ord`：`sort`, `sort_unstable`, `binary_search(&x) -> Result<Int, Int>`（`Ok(一致した位置)` か `Err(順序を保って挿入できる位置)`）
- `T: Clone`：`fill(x)`, `resize(n, x)`, `extend_from_slice(&v)`, `repeat(n)`（要素は複製する）
- `Vec<Vec<T>>`：`concat`

panic はしない。要素の間の位置（`insert`・`split_off`・`truncate`・`resize`・`slice` の範囲）は `[0, len]` に丸め（`insert(100, x)` は末尾に足す）、要素の添字（`remove`・`swap_remove`・`swap`）が範囲外なら `None` か何もしない。

`extend` と `append` は要素を共有する（参照型の要素は複製しない）。`extend_from_slice` は `clone()` した要素を足す。

`for x in v` は毎回 `v.len()` を読み直す。本体で `v`（やその別名）に `push` すると終わらないので注意する。

### `HashMap<K, V>` と `HashSet<T>`

キーは `Hash + Eq + Ord`。

- `HashMap`：`new`, `with_capacity`, `insert(k, v) -> Option<V>`（古い値）, `get(&k) -> Option<V>`, `get_or(&k, d)`, `contains_key`, `remove(&k) -> Option<V>`, `len`, `is_empty`, `clear`, `iter`（`(K, V)`）, `keys`, `values`, `retain`, `extend`
- `HashSet`：`new`, `insert(x) -> Bool`, `contains`, `remove -> Bool`, `len`, `is_empty`, `clear`, `iter`, `extend`, `retain`, `is_subset`, `union`・`intersection`・`difference`（`Vec<T>` を返す）
- 反復は**挿入順**（既存のキーへの `insert` は位置を保ち、`remove` の後の `insert` は末尾）。ハッシュは固定（seed なし）なので結果は決定的。
- 反復は**生きたビュー**で、反復中に追加されたエントリも見え、削除されたエントリは飛ばす。
- 同じバケットへの衝突が 8 を超えると、そのバケットはキーの順序で並べた木になる。わざと衝突させる入力（HashDoS）でも各操作は O(log n)。

## 組み込みメソッド（抜粋）

- `Int`：`to_string`, `abs`, `min`, `max`, `bit_and`, `bit_or`, `bit_xor`, `shl`, `shr`, `ushr`, `cmp`（組み込み）と core（`lib/core/int.kek`）の
  - `Int::max_value()`・`Int::min_value()`（関連定数はまだないので `Int::MAX`・`Int::MIN` の代わり）
  - `pow(e)`（ラップアラウンド。負の `e` は整数除算と同じく切り捨て：`1` は 1、`-1` は ±1、ほかは 0）
  - `checked_add`, `checked_sub`, `checked_mul`, `checked_div`, `checked_rem`, `checked_pow`, `checked_neg`, `checked_abs` は `Option<Int>`（オーバーフロー・0 での除算・`MIN / -1`・負の指数で `None`）
  - `wrapping_add`, `wrapping_sub`, `wrapping_mul`, `wrapping_neg`（演算子と同じ）、`saturating_add`, `saturating_sub`, `saturating_mul`, `saturating_pow`（範囲の端で止まる）
  - `signum`, `is_positive`, `is_negative`, `abs_diff`（`Int` を返し、`Int::MAX` を超えるとラップする）, `clamp(lo, hi)`（`lo > hi` なら `hi`）
  - `rem_euclid`, `div_euclid`（余りは `[0, |b|)`。`%`・`/` と同じく 0 で割ると `x` と `0`）
  - `count_ones`, `count_zeros`, `leading_zeros`, `trailing_zeros`（64bit の 2 の補数。0 は 64）, `is_power_of_two`
- `String`：`len`, `char_at(i) -> Option<Int>`（UTF-16）, `slice(a, b)`, `index_of`, `contains`, `starts_with`, `ends_with`, `split`, `replace`, `trim`, `to_upper`, `to_lower`, `parse_int`, `to_bytes`；`String::from_char(c)`, `String::from_bytes(v)`（組み込み）と core（`lib/core/string.kek`）の
  - `String::new()`, `is_empty`, `to_string`・`to_owned`・`as_str`（そのまま返す）
  - `chars()`：コードポイント（`Int`）のイテレータ（`DoubleEndedIterator` なので `rev()` できる。サロゲートペアは 1 つ、孤立したサロゲートは U+FFFD）。`char_indices()` は `(UTF-16 の位置, コードポイント)`。文字数は `chars().count()`
  - `bytes()`：UTF-8 のバイトのイテレータ（`Vec` は `to_bytes`）
  - `find(pat)`（= `index_of`）, `rfind(pat)` は `Option<Int>`
  - `strip_prefix`, `strip_suffix` は `Option<String>`、`split_once`, `rsplit_once` は `Option<(String, String)>`
  - `trim_start`, `trim_end`（`trim` と同じ空白）, `trim_start_matches(pat)`, `trim_end_matches(pat)`
  - `lines()`（`\n` と `\r\n` で分ける。末尾の改行の後に空行は数えない）, `split_whitespace()`, `splitn(n, sep)`（最大 `n` 個、最後に残り）は `Vec<String>`
  - `repeat(n)`, `eq_ignore_ascii_case(s)`
- `Option`/`Result`：`is_some`, `is_none`, `is_ok`, `is_err`, `unwrap_or`（組み込み）と core の `map`, `and_then`, `and`, `or`, `or_else`, `xor`, `filter`, `unwrap_or_else`, `unwrap_or_default`, `map_or`, `map_or_else`, `ok_or`, `ok_or_else`, `is_some_and`, `is_none_or`, `zip`, `inspect`, `iter`, `flatten`, `transpose`（`Result` は `map_err`, `ok`, `err`, `is_ok_and`, `is_err_and`, `inspect_err` も）。`unwrap`・`expect` はない
- `Bool`：`then(|| x)`, `then_some(x)`
- `Display` を実装した型：`to_string`（`String` にも）
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
