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
| ラベル付きの `break` / `continue` | 両方 | 🚧 | |
| `break value`（式としての `loop`） | Rust | 🚧 | |
| 式としての代入（`Some(x) => n += x,`） | Rust | 🚧 | 現状は文なので腕ではブロックで包む |
| `?` での `From` によるエラー型の変換 | Rust | 🚧 | 現状はエラー型の一致が必要 |
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
| モジュール `mod` / `use` / `pub` | 🚧 | 現状はディレクトリが 1 つの名前空間 |
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
| イテレータの追加のアダプタ（`flat_map` `scan` `partition` `unzip` …） | 🚧 | |
| `HashMap` の entry API | 🚧 | |
| `BTreeMap` / `BTreeSet` / `VecDeque` | 🚧 | |
