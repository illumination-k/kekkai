# generics・trait・コレクション

以前の `Map` はコンパイラが特別扱いする組み込みで、キーは `Int` / `String` に限られ、値は `__Any` に box され、ハッシュに seed がなく HashDoS に弱く、イテレータもなかった。これを、Rust に沿ったユーザー定義の generics と trait の上に作り直した（書き方は [language.md](language.md)）。

## 決定事項

| 項目 | 決定 |
| --- | --- |
| キーの一般化 | `Eq` / `Hash` / `Ord` の trait とユーザー定義の generics で表す |
| タプル | 入れる |
| ハッシュの seed | 持たない（capability の外に乱数源を持たない）。HashDoS は衝突チェーンの木への切り替えで防ぐ |
| 「Hash できるか」 | `Hash` trait を実装しているか |
| クロージャ | capability を捕捉しないものは第一級。捕捉するもの（`transaction` の本体）は第二級のまま |
| 反復中の変更 | 生きたビュー（追加は見え、削除は飛ばす） |
| struct の `Hash` | フィールドがすべて不変の struct にだけ derive できる。フィールドは既定で不変、代入するものは `mut` |
| 名前・構文 | Rust に合わせる（`HashMap`・`HashSet`・`PartialEq`・`Iterator`・`impl Trait for T`・`where` など） |

## 実装の構成

- **core ライブラリ**（`lib/core`）：Rust の `core` にあたる Kekkai のコードで、どのプログラムにも含まれる（`check`・`ir` でも）。コンパイラには `compiler/core_src.kek` として埋め込まれる（`kek embed-prelude lib/core compiler/core_src.kek -as core`）。
  - `cmp.kek`：`Ordering`、`PartialEq`・`Eq`・`PartialOrd`・`Ord` と組み込み型への実装
  - `hash.kek`：`Hash`・`Hasher`・`DefaultHasher`（固定の FNV-1a）
  - `default.kek`：`Default`
  - `iter.kek`：`Iterator` とアダプタ、`Range`・`RangeInclusive`、`Vec` のイテレータ、`FromIterator`・`Sum`・`Product`
  - `hashmap.kek`：`HashMap`・`HashSet`
  - `entry.kek`：`HashMap`・`BTreeMap` の entry API（`Entry`・`OccupiedEntry`・`VacantEntry`）
  - `btree.kek`：`BTreeMap`・`BTreeSet`（AVL 木）、`Bound`・`RangeBounds`
  - `vecdeque.kek`：`VecDeque`（リングバッファ）
  - タプル（1〜8 要素）の比較・ハッシュの実装は、`#[derive]` と同じ生成器（`compiler/derive.kek`）が作る
  - core の関数は使われたものだけが IR に入る（`lower` は利用者の関数を根にして、generic な関数と core を必要に応じて下ろす）。core の自由関数は `__` で始まる名前にして利用者の名前空間を汚さない。
- **単相化**：generics は IR の手前で具体化する（`Lower.queue`、インスタンス名は `name<Int,String>`）。IR に型変数は現れない。
- **trait**：静的ディスパッチのみ。trait のメソッドは `Trait::m`（型パラメータは `Self`、trait のもの、メソッドのものの順）、実装は `<T as Trait>::m` という名前の関数になる。呼び出しは型検査では trait 経由として記録し、lowering で具体型から実装を引く（`chk_dispatch`）。
  - 境界の確認は関数ごとの obligation として集め、推論が終わってから調べる（`chk_check_obls`）。
  - 関連型は `Ty::Assoc` で表し、具体型が分かった時点で実装の定義に正規化する（`chk_norm`）。
  - `==`・`<` などは、`Int` などの組み込みでなければ `PartialEq::eq`・`PartialOrd::lt` などの呼び出しになる。
  - `#[derive]` は実装のソースを生成し、位置を derive 属性に置き換えて解析する。
- **クロージャ**：非関数化。`fn(A) -> R` ごとに enum（クロージャ式ごと・値として使った関数ごとに 1 つの variant、フィールドは捕捉した値）と apply 関数を作る。IR の新しい命令は不要で、Lean の参照インタプリタもそのまま動く。クロージャは純粋なので async 変換の対象にならない。
- **`HashMap`**：エントリを挿入順に持ち、バケットは連鎖（`next`）。連鎖が 8 を超えたバケットはキーの `Ord` による AVL 木にする。削除は穴を残し、表の拡張時に詰める（`epoch` を進める）。イテレータは詰め直しを検出すると、最後に返したエントリの挿入番号（`seq`）を二分探索して位置を付け直す。文字列のハッシュは `String::__hash`（ランタイムの FNV-1a、Lean にも同じ実装）で計算する。
- 組み込みの `Map`（IR の `map.*` 命令、`wasm_coll` の Map 処理、値の box、prelude の `__Map`、Lean の `map.*`）は削除した。

## Rust との違い

- 値（struct・enum・`Vec`・`HashMap`）は参照で共有される。所有権・move・ライフタイムはない。`&T`・`&mut T`・`&self`・`&mut self`・`let mut`・`mut` 引数は Rust と同じ意味で書き換えを制限する（[mutability.md](mutability.md)）が、保証は「この参照からは書き換えられない」までで、別の可変な別名からは変わりうる。`let x` の値は `let mut y = x` で移して書き換えられ、元の `x` からも読める。
- `&mut T` の引数に `&mut v` と書くのは任意（`v` が可変な経路ならよい）。`*x` は何もしない。
- 共有の参照から借用した値を struct に入れる・所有型で返すことはできない（ライフタイムがないため）。`clone()` するか、`&T`・`Vec<&T>`・`Option<&T>` で受け渡す。利用者の型で借用したデータを回すイテレータは作れない（core のイテレータとアダプタは使える）。
- 型の不変性はフィールドの `mut` で表し（`mut` のないフィールドには代入できない）、`Hash` の derive は `mut` フィールドのない struct に限る。
- `HashMap` のキーは `Hash + Eq` に加えて `Ord` が必要（衝突したバケットを木にするため）。反復は挿入順。
- クロージャは純粋で、変数を値で捕捉する（`FnMut` のように捕捉した変数へは代入できない。捕捉した `let mut` の値の中身は書き換えられる）。`Fn`・`FnMut`・`FnOnce` の区別はない。
- trait object（`dyn Trait`）、`impl Trait` の戻り値（`impl Fn` を除く）、ブランケット実装（`impl<T: A> B for T`）、ライフタイムはない。
- 演算子 `+ - * /` は `Int`（と `String` の `+`）だけで、`Add` などの trait はない。
- `HashMap`・`BTreeMap` の `entry` API は `&mut V` の代わりに値を返す（[language.md](language.md#entry-api)）。`HashSet`・`BTreeSet` の集合演算は `Vec` を返す。

## 残課題

- [ ] core の型名（`Range`・`Iter`・`Map` など）を利用者が定義できない（名前空間がない）
- [ ] `IntoIterator` の境界を持つ generic な関数での `for`（関連型 `IntoIter` が `Iterator` であることを表せない）
- [ ] ブランケット実装と、それに伴う重なりの判定
- [ ] クロージャの enum の表現（すべての variant のフィールドを 1 つの struct に並べるので、同じシグネチャのクロージャが多いと値が大きくなる）
- [ ] `HashMap` の性能（旧 `Map` 比で約 1.3 倍の時間）
