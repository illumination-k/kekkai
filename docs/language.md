# Kekkai 言語リファレンス（現行実装）

設計の背景は [design.md](design.md)、generics・trait・コレクションの設計は [generics.md](generics.md)。ここではセルフホストのコンパイラ（`compiler/`、`./kek`）が現在受け付ける言語を説明する。構文と標準ライブラリの名前は Rust に合わせている。

## プログラムの構成

- 1 ファイル、または 1 ディレクトリ（中の `*.kek` すべてが 1 つの名前空間）が 1 プログラム。
- トップレベルは `struct`・`enum`・`trait`・`impl`・`fn` のみ。グローバル変数はない（＝暗黙の権限がない）。
- どのプログラムにも core ライブラリ（`lib/core`：比較・ハッシュ・`Default`・イテレータ・`HashMap`／`HashSet`・個人情報の `Pii`）が含まれる。core の型名と trait 名は予約されている。`__` で始まる名前は core と prelude だけが使える。
- エントリポイントは次のどちらか一つ。
  - `#[handler] fn h(req: Request, db: &Db, ...) -> Response`：Workers の HTTP ハンドラ（`#[handler(idempotent)]` は冪等なハンドラ。下記）
  - `#[main] fn main(args: Vec<String>, fs: &Fs, ...) -> Int`：コマンドラインプログラム（`kek run`）
- `#[test]` 関数は `kek test` がモック capability を渡して実行する。capability 以外の引数（`Int`・`String`・`Vec`・自前の struct/enum など）を取るとプロパティベーステストになり、引数は生成される（`#[test(cases = N)]` でケース数を指定）。

## 型

| 型 | 説明 |
| --- | --- |
| `Int` | 64bit 符号付き整数。演算はラップアラウンド、`x / 0 == 0`、`x % 0 == x`（panic しない） |
| `Bool`, `String`, `()` | 文字列は不変 |
| `(A, B, ...)` | タプル（不変の値）。要素は `t.0`、`(A,)` は 1 要素 |
| `Option<T>`, `Result<T, E>` | `Some`/`None`, `Ok`/`Err` |
| `Vec<T>` | 伸長可能な配列（参照型） |
| `HashMap<K, V>`, `HashSet<T>` | core ライブラリのコレクション（下記） |
| `Pii<T>` | 個人情報。文字列にできない（下記） |
| `struct S<T> { f: T, mut g: T }` | フィールドは既定で不変、`mut` を付けたものだけ代入できる（参照型） |
| `enum E<T> { A, B(T, U) }` | 再帰的に定義してよい |
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
- `#[derive(PartialEq, Eq, PartialOrd, Ord, Hash, Default, Clone)]` を struct・enum に付けられる（`Default` は struct のみ）。`Hash` を導出できるのは `mut` フィールドのない struct だけ。`Pii` を含む型（フィールドの型に `Pii` が現れる）に導出できるのは `PartialEq`・`Eq`・`Clone` だけ。

### 演算子と core の trait

| trait | 内容 |
| --- | --- |
| `PartialEq`, `Eq` | `==`・`!=`。`Int`・`Bool`・`String`・`()` は組み込みの比較 |
| `PartialOrd`, `Ord` | `<`・`<=`・`>`・`>=`（`Int` は組み込み）、`cmp -> Ordering`、`max`・`min` |
| `Hash`, `Hasher` | `x.hash(&mut h)`、`DefaultHasher::new()`、`h.finish()` |
| `Default` | `default() -> Self` |
| `Clone` | `clone(&self) -> Self`：所有する深い複製（下記「可変性」） |
| `Iterator`, `DoubleEndedIterator`, `IntoIterator`, `FromIterator<A>`, `Sum<A>`, `Product<A>` | 下記 |

core は `Int`・`Bool`・`String`・`()`・タプル（8 要素まで）・`Option`・`Result`・`Vec` にこれらを実装している（`Clone` は `HashMap`・`HashSet`・`Pii` にも）。

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

### 冪等なハンドラ（`#[handler(idempotent)]`）

```kek
#[handler(idempotent)]
fn handle(req: Request, db: &Db, log: &Log) -> Response { ... }
```

- 冪等なハンドラと、そこから呼び出しグラフで到達できる関数は、冪等な capability の操作しか使えない。リトライ（同じリクエストの再送、Workers の再実行）で状態が変わらないことを型検査で保証する。
- 冪等とみなす操作：`log.*`、`clock.now_ms`、`net.get`、`db.get`、`db.transaction`、`tx.get`、`tx.put`（同じキーの上書き）、`tx.delete`（2 回目は何もしない）、`tx.commit`・`tx.rollback`、`fs.read`・`fs.list`・`fs.set_cwd`。
- それ以外は冪等でない：`net.post`、`tx.outbox`、`fs.write`・`fs.write_bytes`、`fs.read_line`（入力を消費する）、`random.int`（リトライで別の ID などを作ってしまう）。新しい操作は一覧に加えるまで冪等でないとみなす。
- 違反はハンドラに報告する：``idempotent handler `h` reaches `net.post` via `settle` -> `charge` (payments.kek:12), which is not idempotent: a retry would do it again``。
- 冪等性は操作の種類で判定する。`tx.put` に乱数や時刻を書く・キーの有無で分岐して別の効果を起こす、といった値に依存する性質は見ない。Idempotency-Key で重複を検出する `tx.outbox` のような、実装上冪等なパターンも型では冪等と認めない（`#[handler]` のまま使う）。
- `kek caps` は冪等なハンドラに `#[handler(idempotent)]`、capability を受け取る関数に `idempotent: true|false` を表示する（JSON は全関数の `idempotent`）。`kek assure` は `idempotent` の保証を記録する。
- 実行時の動作は変えない。Workers 側のリトライの設定（Queues・Workflows の再試行など）に使う場合は、`kek caps -json` の `idempotent` を参照する（ビルド出力にはまだ含めない）。

## 個人情報（`Pii<T>`）

```kek
struct User {
    id: Int,
    email: Pii<String>,
}

fn greet(log: &Log, u: User) {
    log.info("hello " + u.email.mask());       // "a****@example.com"
    log.info("user " + u.email.hash());        // 16 桁の16進数
    // log.info(u.email);                      // 型エラー
}
```

`Pii<T>` は core の generic な struct（`lib/core/pii.kek`）で、値を包むだけで中身を文字列として取り出せない。フィールド `__value` は core の外から書けない名前なので、読むことも `Pii { ... }` で作ることもできない。

| 操作 | 内容 |
| --- | --- |
| `Pii::new(x)` | 包む |
| `p.mask() -> String` | `Pii<String>` のみ。先頭の 1 文字と最後の `@` 以降を残し、ほかの文字（コードポイント）を `*` にする：`"alice@example.com"` → `"a****@example.com"`、`"bob"` → `"b**"`。`@` の前が 1 文字ならそれも隠す（`"a@x.com"` → `"*@x.com"`、`"x"` → `"*"`）。`""` は `""` |
| `p.hash() -> String` | `T: Hash`。core の固定（seed なし）のハッシュ（`DefaultHasher`）を 16 桁の小文字の16進数で。等しい値は等しいハッシュになるので、突き合わせや集計のキーに使える |
| `p.expose_unchecked() -> T` | 値そのもの（脱出口） |
| `==`・`!=` | `Pii` どうしの比較（`PartialEq`・`Eq`）。`Pii<String>` と `String` の比較は型エラーで、`expose_unchecked` が要る |

- `Pii` は表示・変換・順序・ハッシュの trait を実装しない。`to_string` も `+`（`Int`・`String` のみ）もなく、`log.info(p)`・`"x" + p`・`Response::text(200, p)`・`tx.put(k, p)`・`p.to_string()` はどれも型エラーになり、エラーには格下げの方法が添えられる。trait の境界を満たさないので generic な関数経由でも漏れない。
- `Hash` を実装しない理由：`HashMap` のキーには `Hash + Eq + Ord` が要るが、`Ord` は `Pii::new(候補)` との比較で二分探索して値を割り出せるので実装しない。`Ord` がなければ `Hash` はキーとして役に立たず、`h.finish()` で整数として値が漏れる経路にしかならない。キーにしたいときは `p.hash()`（記録される格下げ）の結果を使う。
- `==` は残るので、候補を `Pii::new` で包んで比べる総当たりは防げない（最小版の制限。本格版は情報フロー型）。
- 格下げ（`mask`・`hash`・`expose_unchecked` の呼び出し）は `kek caps` に関数ごとに一覧され（`declassify`）、`kek assure` に前提 `pii.declassify` として記録される。関数に `#[declassify(reason = "...", owner = "...", expires = "YYYY-MM-DD")]` を付けると、その関数の格下げの承認者・理由・期限になる（`kekkai.toml` の `[pii] declassify_requires` で必須にでき、`[pii] max_declassify_per_module` でファイルごとの数を制限できる。[assure.md](assure.md)）。
- `#[derive]` は `Pii` を含む型に `PartialEq`・`Eq` だけを導出できる。`kek test` は `Pii` の値を生成しない（ライブラリの型）。

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
- `if c { } else if d { } else { }`、`if let P = e { } else { }`（式）
- `match e { pat => e, ... }`（式、網羅性検査あり、ネスト可）
- `while c { }`, `while let P = e { }`, `for x in a..b { }`, `for mut x in v { }`, `for (i, x) in iter { }`, `break;`, `continue;`, `return e;`
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
