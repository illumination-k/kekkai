# 可変性の追跡

struct・`Vec`・`HashMap` などは参照型で、代入や引数渡しは参照の共有になる。以前は `&T`・`&mut T`・`&self`・`&mut self` は書けるだけで意味を持たず、どの別名からでも中身を書き換えられた。これを Rust と同じ書き方で追跡する。

**実装済み**：検査は `compiler/chk_mut.kek`（接頭辞 `mu_`）、移行ツールは `kek fix`（`compiler/fix_main.kek`）。言語としての説明は [language.md](language.md#可変性) にある。

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 規則 | Rust と同じ。書き換えには `let mut`・`mut` 引数・`&mut T`・`&mut self` が要る |
| 深さ | 深い。読み取り専用の参照からたどった値（フィールド、要素、イテレータの要素、パターンで束縛した値）もすべて読み取り専用 |
| 保証の範囲 | 「この参照からは書き換えられない」。所有権と move はないので、「この値は二度と変わらない」までは保証しない（別の可変な別名からは変わりうる） |
| 抜け道を塞ぐ | 読み取り専用の値から可変な値を作れない（`let mut`・フィールドや要素への格納・所有型での返却・`&mut`/`mut` 引数への受け渡しはエラー）。複製は `clone()` |

## 値の出自（view）

集約型（struct・enum・タプル・`Vec`・`HashMap`・`HashSet`・クロージャ）の式ごとに、出自を 3 つに分ける。`Int`・`Bool`・`String`・`()` は不変なので対象外。

| view | 意味 | どこから |
| --- | --- | --- |
| `own` | 自由。束縛が可変なら書き換えられ、所有する場所（フィールド、コレクション、`let mut`、所有型の返却）に置ける | 所有型の引数・束縛、コンストラクタ、所有型を返す関数の結果、`clone()` |
| `shared` | 借用した読み取り専用の値（中身も含む）。書き換えられず、所有する場所に置けない | `&T` 引数、`&self`、式 `&e`、`&T` を返す関数の結果、`shared` からたどった値 |
| `shared-contents` | 入れ物は新しく書き換えられるが、中の要素は `shared` | `shared` な値を受け取った core の関数が作った新しいコレクション（`collect`・`keys`・`values` など、`#[fresh]` を付けた関数） |

- 書き換え（`push`・`insert`・`set`・`pop`・`remove`・`clear`・`retain`・`extend` などの変更する組み込み操作、`&mut self` のメソッド、`mut` フィールドへの代入）には、対象が `own` か `shared-contents`（入れ物だけ）で、**可変な経路**を通っていることが要る。
- 可変な経路：`let mut` の束縛、`mut x: T` の引数、`&mut T` の引数・束縛、`&mut self`、式 `&mut e`（`e` が可変な経路）、所有型の一時値（`Vec::new().push(1)` は可）、それらのフィールド・要素。
- `let x`（`mut` なし）の束縛は、値が `own` でも書き換えられない（Rust の不変な束縛）。ただし所有する場所へ移すことはできる（`let mut y = x;` は可。Rust の move に相当。元の束縛からも読めるのは Kekkai の違い）。
- `shared` な値は、`&T` 引数・`mut` のない束縛・core の関数への引数（結果は `shared` になる）・比較や表示にだけ使える。

## 関数とメソッド

| 宣言 | 関数の中 | 呼び出し側の引数 |
| --- | --- | --- |
| `x: T` | 不変な束縛（`own`）。移すことはできる | `shared` 以外 |
| `mut x: T` | 可変 | `shared` 以外 |
| `x: &T` | `shared` | 何でも |
| `x: &mut T` | 可変 | 可変な経路の `own`・`shared-contents`、または一時値 |
| `self` / `mut self` / `&self` / `&mut self` | 上と同じ | 受け手に同じ規則 |

- 戻り値：`-> T` で `shared` な値（`&T` 引数からたどった値など）を返すのはエラー（`clone()` するか `-> &T` にする）。`-> &T` の結果は呼び出し側で `shared`、`-> &mut T` の結果は可変（引数のどれかが可変な経路であること）。
- generic な関数でも同じ。`T` が集約型かどうかに関わらず、型パラメータの値は集約型として扱う。
- クロージャ：捕捉した値は捕捉した束縛の view と可変性を保つ。`shared` な値を捕捉したクロージャは `shared`。
- core と prelude も同じ規則で検査する。イテレータは要素の view を持ち、`shared` なコレクションの `iter()` は `shared` な要素を返すイテレータになる。イテレータ自体の位置（カーソル）は `own` なので、`next` などは呼べる。

## `clone()`

- core に `trait Clone { fn clone(&self) -> Self; }`。`#[derive(Clone)]` は全フィールドを `clone()` する。
- `Int`・`Bool`・`String`・`()`・タプル・`Option`・`Result`・`Vec<T: Clone>`・`HashMap`・`HashSet` に実装する。`clone()` の結果は `own`（深い複製）。
- `Pii<T>` は `T: Clone` なら `Clone`。

## 移行

- `kek fix [-w] <paths>`：書き換えている束縛と引数に `mut` を付ける（不足している `&mut`・`let mut`・`mut` 引数を補う）。`-w` で上書き、なしなら差分を表示する。コンパイラ・core・prelude・testdata・examples をこれで移行する。
- 移行できない箇所（`shared` な値を所有する場所に置いている）はエラーとして残り、手で `clone()` や `&T` に直す。

## 診断

Rust に近い文言にする。

```
x.kek:3:5: cannot mutate `v`: it is not declared as mutable (write `let mut v`)
x.kek:7:9: cannot mutate `xs` through a shared reference `&Vec<Int>` (take `&mut Vec<Int>`)
x.kek:9:14: cannot store a borrowed value in `out`: it comes from the shared reference `item` (use `item.clone()`)
x.kek:12:5: cannot return a borrowed value as `Vec<Int>`: it comes from `&self` (return `&Vec<Int>` or clone it)
```

## 実装で決めたこと

上の設計を実装する際に決めたこと・変えたこと。

### 検査の置き場所

- 型検査の後の独立したパス（`compiler/chk_mut.kek`、トランザクションの線形性検査 `chk_linear.kek` と同じ形）。`ChkInfo`（式ごとの型、メソッドと呼び出しの解決、束縛、クロージャの捕捉）を読み、AST を一度歩く。型エラーがあるときは走らない（篩型の検査と同じ）。可変性のエラーがあっても篩型の検査は走る。
- 診断は型エラーと同じく `kek check` のエラーになる。`kek check -json` では新しい phase `mut` で報告する（型の問題ではなく、`kek fix` で直せるものが多いので分ける）。
- パスは診断ごとに直し方（`mut` を入れる位置と種類）を持ち、`kek fix` はそれを使う（文言を解析しない）。

### どの型が対象か

- view が意味を持つのは、**書き換えられる状態に届く型**だけ：`Vec`、`mut` フィールドを持つ struct、それらを（フィールド・要素・ペイロード・型引数として）含む型、型パラメータ（設計どおり集約型として扱う）、クロージャ。`Int`・`String`、不変なフィールドだけの struct（`AstPos` のような値）、`Option<Int>` などは共有しても害がないので、借用からたどっても自由に保存・返却できる。設計の「集約型」をこの意味に絞った（移行で要る `clone()` がなくなる）。struct・enum ごとの判定は宣言の不動点で一度だけ計算する。
- `shared` な値でも、外側の型が自分では書き換えられないもの（`Option`・`Result`・タプル・`mut` フィールドのない struct）は `shared-contents` と同じに扱う（書き換えられるのは中身だけで、中身はどちらでも `shared`）。また `shared-contents` でも中身が書き換えられない型なら `own` と同じ（`tasks.keys().collect()` の `Vec<String>` は自由に使える）。こうして `-> Option<&T>` で `v.get(i)` を返したり、`Vec<&T>` に `collect` したりできる。
- 型に書いた `&` は view の要求になる：`&T` は `shared` を受け取り、中に `&` を含む型（`Vec<&T>`・`Option<&T>`）は `shared-contents` を受け取る。引数・戻り値・`let` の型注釈・struct のフィールドで同じ。`let mut x: &T = ...` は付け替えられる共有参照。

### イテレータと view

- イテレータは「カーソル（自分の状態）」と「要素」に分かれる。`shared` なコレクションの `iter()` は `shared-contents`：イテレータ自体は新しく作られた値なので `next(&mut self)` で進められる（`let mut it = v.iter()` が要る）が、要素は `shared`。
- これを core の関数の宣言で表すのが `#[fresh]`：引数に `shared`（か `shared-contents`）があるとき、普通の core の関数の結果は `shared` だが、`#[fresh]` の関数の結果は `shared-contents` になる。付けたのは `Vec::iter`、`HashMap::iter`・`keys`・`values`、`HashSet::iter`・`union`・`intersection`・`difference`、`IntoIterator::into_iter`、`FromIterator::from_iter`、`Iterator` のアダプタ（`map`・`filter`・`filter_map`・`enumerate`・`zip`・`chain`・`take`・`skip`・`take_while`・`skip_while`・`step_by`・`peekable`・`rev`）と `collect`。trait のメソッドに付けると、その実装すべてに効く。
- そのため `next`・`peek`・`nth` など要素を返すもの、`max`・`find` などの消費は（`#[fresh]` でないので）`shared` な要素（を包んだ `Option`）を返す。`count`・`sum` のように書き換えられない型を返すものは view を持たない。
- `for x in e`：`e` が `Vec` なら `x` は `e` の要素の view。`IntoIterator` のコレクションは `into_iter`（`#[fresh]`）を通るので同じ。イテレータそのものを回すときは、それを進めるので `shared` なイテレータ（`&Range` の引数など）は回せない（`for` はイテレータを消費するので、`let` の束縛が可変である必要はない）。
- `collect` で集めたコレクションは `shared-contents`：`let mut v: Vec<&Item> = items.iter().collect()` には要素を足せるが、要素は書き換えられず、所有型 `Vec<Item>` としては返せない（`map(|x| x.clone())` を挟む）。
- 利用者の struct で借用したデータを回すイテレータは作れない（借用した値を struct に入れられないため。ライフタイムがないので）。core のアダプタを組み合わせるか、`clone()` する。

### core と prelude

- core と prelude の本体も同じ規則で検査する：書き換え（可変な経路、`shared` を通さない）はすべて同じ。
- ただし借用したデータから値を作ること（struct リテラル・コンストラクタ・`let`・戻り値・局所的なコレクションへの格納）は許す。core の関数の結果は、呼び出し側で引数の view に汚染される（設計の「結果は `shared`」）ので、`Iter { v: *self, .. }` のように `&self` を包んで返しても安全。引数（`self` を含む）が根の場所への格納だけは検査する（`extend` が借用した要素を受け手に入れるなど）。
- core の関数の呼び出しでは、引数は何でも渡せる（結果が汚染される）。ただし `mut`・`&mut`・`mut self`・`&mut self` で受け取る位置には `shared` を渡せない。`&mut self` のメソッド（`insert`・`push`・`extend` など）の他の引数は受け手に格納されるので、受け手が `own` なら `own` でなければならない（受け手が `shared-contents` なら `shared` も可）。
- `#[fresh]` は core と prelude でだけ書ける（それ以外ではエラー）。メソッドにも付けられるように、メソッドの属性を構文として受け付ける（`#[fresh]` 以外はエラー）。
- `clone()`（`Clone::clone` とその実装）の結果は常に `own`。利用者の `Clone` の実装は通常の規則で検査されるので、`&self` をそのまま返すことはできない。

### クロージャ

- 捕捉した束縛の view と可変性を保つ。`let v` を捕捉して `v.push` すればエラーで、`let mut v` にする（クロージャは捕捉した**変数**には代入できないまま。書き換えられるのは値の中身）。
- クロージャの値の view は捕捉した値の最大。`shared` を捕捉したクロージャはフィールドなどに置けない。クロージャの値の呼び出し結果は、クロージャと引数の view で汚染される。ただしクロージャが捕捉したものを外に渡せるのは結果を通してだけ（捕捉した変数には代入できず、捕捉した値を可変な場所へ入れることは本体で検査される）なので、結果の型が書き換えられる状態に届かないクロージャ（`|x| x + it.count` など）は捕捉にかかわらず `own` とした（`apply(|x| x + it.count, 1)` のように利用者の関数に渡せる）。
- クロージャの**引数**の view は、クロージャ式がどこに書かれたかで決める：
  - core の関数に直接渡したとき：他の引数（受け手を含む）に `shared` か `shared-contents` があれば `shared`、なければ `own`。core はクロージャを引数から得た値で呼ぶので、これで足りる（`items.iter().for_each(|x| ...)` の `x` は `items` が `&Vec<T>` なら `shared`）。
  - 利用者の関数に直接渡したとき：受け取る引数の型（`f: fn(&T)`、`F: Fn(&T)`、`impl Fn(&T)`）の `&` に従う。
  - それ以外（`let f = |x| ...` など）：`own`。引数に `&T` と書けば `shared`。
  - `|mut x| ...` は可変な引数。`shared` を受け取る位置ではエラー。
- 関数の値（クロージャ、値として使った関数）の呼び出し `f(a)` は、引数を `own` で渡す（`fn(&T)` の位置だけ `shared` も可）。`shared` なデータの上で core の関数に関数の値を渡すとき（`items.iter().map(take)`）は、その引数が `&T` でなければエラー（クロージャ式にする）。値として使う関数は `&mut` の引数を持てない（呼び出し側で可変な経路を確かめられないため）。

### 呼び出しと `&mut`

- `&mut T` の引数に渡すとき、式 `&mut e` は書いても書かなくてもよい（`add(&mut v, 1)` も `add(v, 1)` も可）。要るのは、渡す値が可変な経路で `shared` でないこと。`&e`（共有の借用）を `&mut T` に渡すのはエラーで、`kek fix` が `&mut e` に直す。
- `&mut T`（中に `&` のない型）の引数に `shared-contents` は渡せない（関数が中身を書き換えられるため）。
- trait の実装のメソッドは、trait の宣言が受け取るものを受け取らなければならない：宣言が `&self`・`x: &T` なら、実装も `&self`・`&T`（`&mut self` や所有では、`shared` な受け手を書き換えたり保存したりできてしまう）。逆（宣言が `self`、実装が `&self`）は可。trait のメソッドの呼び出しは宣言のシグネチャで検査する。

### その他の決定

- 所有権がないので `let x` の不変性は浅い：`let mut y = x` で移して書き換えられ（設計どおり）、`x.get(0).unwrap().push(1)` のような所有型の一時値も可変な経路。保証は `shared` な値に対してのもの。
- パターン（`match`・`if let`・`let (a, b)`・`for (k, v)`）で束縛した値は照合した値の要素の view を持ち、`mut` を付けなければ不変（`Some(mut s) => s.push(..)`、`for mut x in v`）。`shared` を `mut` の束縛に入れるのはエラー。
- 診断は位置と文言の組で重複を除く。同じ束縛への書き換えが複数あれば、それぞれの位置で報告する（`kek fix` は束縛ごとに 1 回直す）。

### `kek fix`

- 各パスを 1 つのプログラムとして（`kek check` と同じ。`lib/core`・`lib/prelude` のディレクトリは core・prelude として）検査し、パスの診断が持つ直し方を適用する：`let x` → `let mut x`、`x: T` → `mut x: T`、`self` → `mut self`、`Some(x)` → `Some(mut x)`、`for x` → `for mut x`、`|x|` → `|mut x|`、書き換えられる `&T` の引数 → `&mut T`（`&self` → `&mut self`）、`&mut T` に渡した `&e` → `&mut e`。
- 直したら検査し直し、変わらなくなるまで繰り返す（`&T` を `&mut T` にすると呼び出し側の束縛に `mut` が要る、など）。編集は文字列への挿入なので、コメントと整形は保たれる（行が長くなったら `kek fmt -w`）。
- trait のメソッド（の宣言と実装）の `&self`・`&T` はシグネチャの一部なので書き換えない（報告だけ）。直せないエラー（借用した値の保存・返却など）と、型エラーなど他のエラーは一覧にして終了コード 1。篩型の検査は止めて走らせる（修正を妨げないため）。
- `-w` なしでは変更を差分で表示し、ファイルは変えない。

### 移行とブートストラップ

コンパイラは自分自身で書かれているので、2 段階で入れた。

1. 構文（`mut` 引数・`mut self`・`|mut x|`・`for mut x`・メソッドの属性）、`Clone`、`#[fresh]`、検査パスと `kek fix` を入れ、規則は強制しない（`mu_enforce()` が偽：パスは `kek fix` のためにだけ走る）。core と prelude は stage1 だけが構文解析するので、この段階で移行した。
2. `./kek bootstrap-update`：bootstrap が新しい構文を受け付けるようになる。
3. `kek fix -w` でコンパイラ・testdata・examples・tests・lean のテストを移行し、残りを手で直し、規則を強制する。
4. もう一度 `./kek bootstrap-update`：bootstrap 自身が規則を強制する（`tests/suites/bootstrap.sh` の `mutability` が確かめる）。

`kek fix` による変更は 1,384 か所（コンパイラ 1,176、core 35、prelude 71、testdata・examples・tests・lean 102）。手で直したのは、`&self` から所有型を返していたテストの 2 プログラム（`testdata/run/assoc_types.kek`・`generics.kek`：受け手を `self` に）、ランダムなプログラムの生成器（`tests/difftest/gen.kek`：コレクションと struct はいつも `let mut`）、プロパティテストのハーネスの生成（`compiler/pbt.kek`：`let mut __out`）。`clone()` は移行には要らなかった。

### 実行時

静的な検査だけで、IR・コード生成・Lean の参照インタプリタは変わらない（`clone()` は普通の関数）。

### `readonly`

`kek caps -json` は関数ごとに `readonly`（入力を何も書き換えない）を出し、`kek assure` は書き換えられる状態を持つ引数を受け取る関数にこれを保証 `readonly`（根拠 `type`）として記録する。判定：`mut`・`&mut`・`mut self`・`&mut self` の引数がなく、所有型の引数をすべて `shared` とみなしても本体が規則を満たす（書き換えも、可変な場所への移動もしない。返すのはよい）。
