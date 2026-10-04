# P1：篩型・個人情報の最小版・冪等性

設計の背景は [design.md](design.md)（保証する性質の P1、篩型、個人情報の最小版）。ここでは決定事項と実装の分担を書く。

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 篩型の書き方 | `where` 節に述語を書く（trait の境界と混在できる）。型の別名 `type Port = Int where 0 < self && self < 65536;` |
| 述語の言語 | 線形整数算術（QF\_LIA）：整数リテラル、`Int` の引数・局所変数、不変なフィールド（`p.x`）、`v.len()`（`Vec`・`String`）、`+`・`-`、定数との `*`、比較、`&&`・`\|\|`・`!` |
| ソルバ | 自作の QF\_LIA ソルバ（Omega test）。Kekkai で書き、セルフホストのコンパイラに含める（Z3 には依存しない）。ソルバの正しさは公理扱い |
| 整数の意味論 | 実行時は 64bit のラップアラウンドのまま。推論は数学的な整数で行い、`+ - *` ごとに「オーバーフローしない」という検証条件を出す |
| オーバーフロー | 証明で排除する。証明できない箇所は既定では警告（lint）、`kekkai.toml` の `[refine] overflow = "error"` でエラー。`checked_add` などの `Option` を返す演算も用意する |
| ゼロ除算 | `/`・`%` は除数が 0 でないという検証条件を出す。扱いは overflow と同じ（`[refine] division`）。実行時の意味（`x / 0 == 0`）は変えない |
| 添字 | 新しい式 `v[i]`（`Vec`）は `0 <= i && i < v.len()` の証明を要求し、要素を直接返す（証明できなければ型エラー）。`get(i) -> Option` はそのまま |
| 証明できないとき | エラーには反例（ソルバのモデル）と、使った事実を付ける |

## 構文

```kek
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
{ ... }

type Port = Int where 0 < self && self < 65536;
type Index = Int where 0 <= self;

fn listen(p: Port) { ... }
```

- `where` の各項目は、トップレベルに 1 つの `:`（`::` ではない）を含めば trait の境界、そうでなければ述語。
- `result` を含む述語は事後条件、それ以外は事前条件。
- 型の別名は透過的で、`Port` は `Int` として使える。`Int` から `Port` への変換（引数・戻り値・型注釈付きの `let`・フィールド）で述語の証明を要求する。`Port` の値からは述語を事実として得る。
- 篩型の述語は `kek fmt` で整形され、`kek search` のシグネチャにも表示される。

## 検証条件と事実

検証条件（VC）を出す箇所：

| 箇所 | 条件 |
| --- | --- |
| 呼び出し | 呼び出し先の事前条件（引数で置き換えたもの） |
| `return`・本体の末尾 | 事後条件、戻り値の型の別名の述語 |
| 篩型の別名への変換 | その述語 |
| `v[i]` | `0 <= i && i < v.len()` |
| `/`・`%` | 除数 `!= 0`（lint） |
| `+`・`-`・`*` | 結果が `[-2^63, 2^63)` に収まる（lint） |

各点で使える事実：

- 事前条件、篩型の別名の引数の述語、`v.len() >= 0`
- `if`・`while` の条件（否定は `else` 側）、`match` の整数パターン、`&&`・`||` の短絡
- `for i in a..b` の `a <= i && i < b`（`..=` も）
- 不変な `let x = e`（`e` が線形）の `x == e`、可変変数は代入ごとに新しい版
- ループ：本体で代入される変数はループの先頭で忘れる。ただし初期値から非負の定数だけ増える（減る）変数には下限（上限）を残す
- 呼び出しの結果：呼び出し先の事後条件、戻り値の型の別名の述語
- `v.len()` の事実は、`v` を変更しうる操作（`push`・`pop`、`v` を渡す呼び出し、利用者の関数の呼び出し）の後で忘れる（値は参照で共有されるので保守的に扱う）

## ソルバの API（`compiler/smt.kek`、prefix `smt_`）

```kek
// sum(coef * atom) + k
struct SmtMono { coef: Int, atom: String }
struct SmtLin { terms: Vec<SmtMono>, k: Int }

enum SmtF {
    True,
    False,
    Le(SmtLin),             // lin <= 0
    Eq(SmtLin),             // lin == 0
    Not(SmtF),
    And(Vec<SmtF>),
    Or(Vec<SmtF>),
    Named(String, SmtF),    // 名前付きの仮定（unsat core に使う）
}

enum SmtResult {
    Valid(Vec<String>),            // 証明できた。使った仮定の名前（unsat core）
    Invalid(Vec<SmtBinding>),      // 反例（atom への整数の割り当て）
    Unknown(String),               // 予算切れ・係数のオーバーフローなど
}
struct SmtBinding { atom: String, value: Int }

fn smt_prove(hyps: Vec<SmtF>, goal: SmtF) -> SmtResult
```

- atom は不透明な名前（`"x"`、`"x#2"`、`"len(v)"`）。すべて整数。
- 判定は「仮定 ∧ ¬目標」が整数で充足不能か。論理結合子は小さな DPLL で場合分けし、線形の連言は Omega test で解く。
- 予算（場合分けの数・係数の大きさ）を超えたら `Unknown`。型検査器は `Unknown` を「証明できない」として扱う。

## `kek` との統合

- `kek check`：篩型の違反は型エラー（`phase: "refine"`）、overflow・division は `phase: "lint"` の警告（設定でエラー）。
- `kek assure`：関数ごとに `refine.no_overflow`・`refine.no_div_zero`・`refine.index_safe` を保証として記録する（証明できた関数だけ。根拠は `smt`）。
- `kek test`：引数が篩型の別名のプロパティテストは、述語を満たす値だけを生成する（棄却法から始める）。
- `kek mutate`：篩型の証明に失敗する変異体は「篩型で検出」として数える。
- Lean：コア計算に事前条件付きの呼び出し・除算・固定長配列の添字を加え、「検証条件が成り立つプログラムは除算と添字で行き詰まらない」を証明する（ソルバは仮定として与える）。

## 個人情報の最小版（`Pii<T>`）

- core の型 `Pii<T>`：`Pii::new(x)` で包む。`to_string`・連結・`==` 以外の比較・`Hash` はない（`String` として取り出せない）。
- 格下げ：`mask() -> String`（`String` の場合、先頭 1 文字と `@` 以降を残す）、`hash() -> String`（固定のハッシュ）、`expose_unchecked() -> T`（脱出口）。格下げの呼び出しは `kek caps` に一覧され、`kek assure` に前提（`pii.declassify`）として記録され、`[pii] max_declassify_per_module` で上限を設けられる。
- `#[derive]` は `Pii` のフィールドを持つ struct に `Hash`・`PartialEq` 以外を導出しない。

## 冪等性（`#[handler(idempotent)]`）

- `#[handler(idempotent)]` のハンドラ（と、そこから呼ばれる関数）は、冪等でない capability の操作を呼べない：`Net.post`、`tx.outbox`、キーを指定しない書き込みなど。`Net.get`・`Db.get`・`tx.put`（同じ値の上書き）は冪等とみなす。
- 冪等性は capability と同じく呼び出しグラフを通じて検査し、`kek caps` と `kek assure`（`idempotent`）に現れる。
