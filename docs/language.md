# Kekkai 言語リファレンス（現行実装）

設計の背景は [design.md](design.md)、generics・trait・コレクションの設計は [generics.md](generics.md)。ここではセルフホストのコンパイラ（`compiler/`、`./kek`）が現在受け付ける言語を説明する。構文と標準ライブラリの名前は Rust に合わせている。

## プログラムの構成

- 1 ファイル、または 1 ディレクトリ（中の `*.kek` すべてが 1 つの名前空間）が 1 プログラム。
- トップレベルは `struct`・`enum`・`trait`・`impl`・`fn` のみ。グローバル変数はない（＝暗黙の権限がない）。
- どのプログラムにも core ライブラリ（`lib/core`：比較・ハッシュ・`Default`・イテレータ・`HashMap`／`HashSet`・ラベル付きの値の `Labeled`・権限の `Can`・シリアライズの `Value`／`Json`／`Toml`）が含まれる。core の型名と trait 名は予約されている。`__` で始まる名前は core と prelude だけが使える。
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
| `Labeled<L, T>` | ラベル付きの値（個人情報は `Labeled<PII, T>`）。文字列にできない（下記） |
| `Can<A, r>` | 資源 `r`（変数）への操作 `A` の権限（下記） |
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
- `#[derive(PartialEq, Eq, PartialOrd, Ord, Hash, Default, Clone, Serialize, Deserialize)]` を struct・enum に付けられる（`Default` は struct のみ）。`Hash` を導出できるのは `mut` フィールドのない struct だけ。`Labeled` を含む型（フィールドの型に `Labeled` が現れる）に導出できるのは `Clone` と `Deserialize` だけ。

### 演算子と core の trait

| trait | 内容 |
| --- | --- |
| `PartialEq`, `Eq` | `==`・`!=`。`Int`・`Bool`・`String`・`()` は組み込みの比較 |
| `PartialOrd`, `Ord` | `<`・`<=`・`>`・`>=`（`Int` は組み込み）、`cmp -> Ordering`、`max`・`min` |
| `Hash`, `Hasher` | `x.hash(&mut h)`、`DefaultHasher::new()`、`h.finish()` |
| `Default` | `default() -> Self` |
| `Clone` | `clone(&self) -> Self`：所有する深い複製（下記「可変性」） |
| `Iterator`, `DoubleEndedIterator`, `IntoIterator`, `FromIterator<A>`, `Sum<A>`, `Product<A>` | 下記 |

core は `Int`・`Bool`・`String`・`()`・タプル（8 要素まで）・`Option`・`Result`・`Vec` にこれらを実装している（`Clone` は `HashMap`・`HashSet`・`Labeled` にも）。

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

- `Labeled` は表示・変換・比較・順序・ハッシュの trait を実装しない。`log.info(l)`・`"x" + l`・`Response::text(200, l)`・`tx.put(k, l)`・`l.to_string()`・`l == m`・`if` の条件はどれも型エラーになり、エラーには `map`・`zip`・`and_then` と格下げの方法が添えられる。trait の境界を満たさないので generic な関数経由でも漏れない。違うラベルの値は `zip` で組み合わせられない。
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
