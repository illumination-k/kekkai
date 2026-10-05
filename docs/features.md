# Go / Rust との言語機能の対照

Kekkai は Rust の構文に合わせ、Go の「サーバーを書くのに必要なものが揃っている」実用性を目標にしている。この文書は Go と Rust にあって Kekkai に必要な機能を洗い出し、対応状況と意図的に入れないものを記録する（2026-10 時点）。

凡例：✅ 実装済み　🚧 未実装（予定）　⛔ 入れない（設計上の理由）

## 式・文

| 機能 | Rust / Go | 状態 | 備考 |
| --- | --- | --- | --- |
| 複合代入 `+=` `-=` … | 両方 | ✅ | 変数・`mut` フィールド・`v[i]` |
| 添字への代入 `v[i] = x` | 両方 | ✅ | 読み出しと同じく範囲の証明が要る（篩型） |
| `loop` | Rust（Go は `for {}`） | ✅ | `break` のない `loop` は発散する |
| `let ... else` | Rust | ✅ | else は発散しなければならない |
| ビット演算子 `& \| ^ << >>`、`!x` | 両方 | ✅ | `Int` のみ |
| 16進・8進・2進・`_` 区切りのリテラル | 両方 | ✅ | |
| 文字リテラル `'a'`、`b'a'` | 両方 | ✅ | `char` 型はなく、コードポイントの `Int` |
| ラベル付きの `break` / `continue` | 両方 | ✅ | `'a: for` / `'a: while` / `'a: loop`。ラベルの隠蔽はエラー |
| `break value`（式としての `loop`） | Rust | ✅ | `loop` だけ。ブロック末尾の `loop` はその値 |
| match の腕の本体に代入・`break` など | Rust | ✅ | `Some(x) => total += x,`、`None => break,` |
| `?` のエラー型の `From` 変換 | Rust | ✅ | core の `From<T>`（`Into` はない） |
| ラベル付きのブロック `'a: { }` | Rust | 🚧 | |
| let チェーン `if let P = e && c` | Rust 2024 | 🚧 | |
| `as` によるキャスト | 両方 | ⛔ | 数値型が `Int` だけなので不要 |
| `defer` | Go | ⛔ | 線形な `Tx` と RAII 的な後始末は型で表す |

## パターン

| 機能 | 状態 | 備考 |
| --- | --- | --- |
| ガード `p if c =>` | ✅ | ガード付きの腕は網羅性に数えない |
| or パターン `A \| B` | ✅ | 入れ子も可。各選択肢は同じ変数を束縛する |
| 範囲パターン `1..=5`、`1..5` | ✅ | |
| `@` 束縛 `n @ 1..=5` | ✅ | |
| struct パターン `P { x, .. }` | ✅ | `let`・`match`・`if let`・`for` |

## 項目（アイテム）

| 機能 | 状態 | 備考 |
| --- | --- | --- |
| `const` と関連定数（`Int::MAX`） | ✅ | 純粋な式のみ |
| タプル struct・ユニット struct | ✅ | |
| struct 形式の enum の variant | ✅ | |
| モジュール・`use`・`pub` | ✅ | サブディレクトリがモジュール（Go と同じく `mod foo;` は書かない）。メソッドとフィールドの `pub` も検査する（[modules.md](modules.md)） |
| ファイル内の `mod foo { ... }`、`pub use`、`pub(crate)` | 🚧 | |
| `static` / グローバル変数 | ⛔ | 暗黙の権限になる（capability の原則） |

## 型・trait

| 機能 | 状態 | 備考 |
| --- | --- | --- |
| 演算子の多重定義 `Add` `Sub` `Mul` `Div` `Rem` `Neg` | ✅ | |
| `Display` / `Debug`、`#[derive(Debug)]`、`to_string` | ✅ | `Labeled` は実装しない（情報フロー） |
| 浮動小数点数 `f64` | 🚧 | IR・Lean・コード生成・数値の表示が要る |
| `dyn Trait`、`Box<dyn Trait>` | ✅ | プログラム中で変換される具体型の enum とディスパッチ関数に変換する。`dyn A + B` は未対応 |
| `impl Trait`（引数・戻り値） | ✅ | trait メソッドの戻り値（RPITIT）は未対応 |
| trait の関連定数 | 🚧 | |
| goroutine / channel、`async` | ⛔ | I/O はコンパイラがステートマシンに変換する。並行性は Workers に任せる |
| 所有権・ライフタイム | ⛔ | 可変性の追跡（docs/mutability.md）で代える |

## マクロ

| 機能 | 状態 | 備考 |
| --- | --- | --- |
| `format!`（`{}` `{:?}` `{name}` 幅・精度・基数） | ✅ | |
| `vec!` `matches!` | ✅ | |
| `assert!` `assert_eq!` `assert_ne!` `panic!` `unreachable!` `todo!` | ✅ | `#[test]` 関数の中だけ（本番のコードは panic しない） |
| `println!` | ⛔ | 出力は `&Log` の capability を通す（`log.info(format!(..))`） |
| ユーザー定義のマクロ | ⛔ | |

## 標準ライブラリ

| 機能 | 状態 | 備考 |
| --- | --- | --- |
| `Option` / `Result` のコンビネータ | ✅ | `map` `and_then` `ok_or` `map_err` … `unwrap` / `expect` はない |
| `Vec` の操作（`sort` `contains` `insert` `remove` `dedup` `binary_search` …） | ✅ | |
| `Int` の操作（`pow` `checked_*` `saturating_*` `rem_euclid` …） | ✅ | |
| `String` の操作（`chars` `lines` `split_once` `strip_prefix` `repeat` …） | ✅ | |
| イテレータのアダプタ（`flat_map` `flatten` `scan` `inspect` `map_while` `fuse` `cycle` `cloned`） | ✅ | `scan` のクロージャは `Option<(状態, 要素)>` を返す |
| イテレータの消費（`reduce` `try_fold` `partition` `unzip` `min_by` `max_by` `is_sorted` `eq` `cmp` …） | ✅ | `try_fold` は `Option` / `Result`。`partition` / `unzip` は `Vec` を返す |
| `collect` / `sum` で `Option<Vec<T>>`・`Result<Vec<T>, E>`、コードポイントから `String` | ✅ | |
| `HashMap` の entry API（`or_insert` `or_insert_with` `or_default` `and_modify` `Occupied` / `Vacant`） | ✅ | `&mut V` の代わりに値を返し、`and_modify` は `fn(V) -> V` |
| `BTreeMap` / `BTreeSet`（順序付き、`range` `first_key_value` `pop_first` entry API） | ✅ | AVL 木で O(log n)。反復は生きたビュー |
| `VecDeque`（`push_front` `pop_front` `push_back` `pop_back` …） | ✅ | リングバッファ |
