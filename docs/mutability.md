# 可変性の追跡

struct・`Vec`・`HashMap` などは参照型で、代入や引数渡しは参照の共有になる。これまで `&T`・`&mut T`・`&self`・`&mut self` は書けるだけで意味を持たず、どの別名からでも中身を書き換えられた。これを Rust と同じ書き方で追跡する。

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
