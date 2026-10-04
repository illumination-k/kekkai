# P1：篩型・個人情報の最小版・冪等性

設計の背景は [design.md](design.md)（保証する性質の P1、篩型、個人情報の最小版）。ここでは決定事項と実装の分担を書く。

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 篩型の書き方 | `where` 節に述語を書く（trait の境界と混在できる）。型の別名 `type Port = Int where 0 < self && self < 65536;` |
| 述語の言語 | 線形整数算術（QF\_LIA）：整数リテラル、`Int` の引数・局所変数、不変なフィールド（`p.x`）、`v.len()`（`Vec`・`String`）、`+`・`-`、定数との `*`、比較、`&&`・`\|\|`・`!` |
| ソルバ | 自作の QF\_LIA ソルバ（Omega test）。Kekkai で書き、セルフホストのコンパイラに含める（Z3 には依存しない）。ソルバの正しさは公理扱い |
| 整数の意味論 | 実行時は 64bit のラップアラウンドのまま。推論は数学的な整数で行い、`+ - *` ごとに「オーバーフローしない」という検証条件を出す |
| オーバーフロー | 証明で排除する。証明できない箇所は既定では警告（lint）、`kekkai.toml` の `[refine] overflow = "error"` でエラー。既定（`"auto"`）では篩型を使う関数だけで検査する（下の「篩型（実装済み）」）。`checked_add` などの `Option` を返す演算は未実装 |
| ゼロ除算 | `/` は除数が 0 でないことと `MIN / -1` でないこと、`%` は除数が 0 でないことを検証条件にする（Lean のモデルが `d != 0` だけでは `MIN / -1` のオーバーフローを見逃すことを見つけた。`MIN % -1` は 0 で、はみ出さない）。扱いは overflow と同じ（`[refine] division`）。実行時の意味（`x / 0 == 0`）は変えない |
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
| `/` | 除数 `!= 0`、かつ `!(x == MIN && y == -1)`（lint） |
| `%` | 除数 `!= 0`（lint） |
| `+`・`-`・`*`・単項 `-` | 結果が `[-2^63, 2^63)` に収まる（lint。実装は `\|結果\| <= 2^60` を示す。下記） |

各点で使える事実：

- 事前条件、篩型の別名の引数の述語、`v.len() >= 0`
- `if`・`while` の条件（否定は `else` 側）、`match` の整数パターン、`&&`・`||` の短絡
- `for i in a..b` の `a <= i && i < b`（`..=` も）
- 不変な `let x = e`（`e` が線形）の `x == e`、可変変数は代入ごとに新しい版
- ループ：本体で代入される変数はループの先頭で忘れる。ただし初期値から増えるだけ（減るだけ）の変数には下限（上限）を残す（実装は「増えるだけ」を定数の増分に限らず、ソルバで確かめる。下記）
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

### ソルバの実装

- 否定標準形：リテラルは `lin <= 0` と `lin == 0` だけ。`!(lin <= 0)` は `-lin + 1 <= 0`、`!(lin == 0)` は `lin + 1 <= 0 || -lin + 1 <= 0`。変数のないリテラルは畳み込み、根が `False` なら（目標が自明に真など）即 `Valid`。否定で定数が `Int` の最大値を超えるとき（`!(x + y <= MAX)`）は最大値に丸める。弱いリテラルになるので `Valid` は健全なままで、モデルが元の式を満たさなければ `Unknown("overflow")`。
- DPLL：単位制約の連言を各節点で理論に解かせる（部分割り当ての検査）。そのモデルが残りの選言をすべて満たせば充足。そうでなければ満たされていない選言のうち子の少ないもので場合分けする。
- 理論（Omega test、Pugh 1991）：gcd による正規化（不等式は定数を切り上げ、等式は割り切れなければ矛盾）→ 等式の消去（係数 ±1 なら代入、なければ `mod^` で新しい変数を導入）→ 係数が同じ不等式はきつい方だけ残し、逆向きの組は矛盾か等式にする（1 変数の上下限の矛盾はここで見つかる）→ すべて 1 変数なら区間で即答 → 片側にしか現れない変数は制約ごと落とす → 残りは Fourier–Motzkin。消去する変数は厳密（片側の係数がすべて 1）なものを優先し、厳密でなければ real shadow（充足不能なら終わり）、dark shadow（充足なら終わり）、splinter の順。
- unsat core：各制約が由来する仮定のラベルの集合（ビット集合、62 個目以降はまとめて 1 ビット）を持ち、導出した制約は和集合を持つ。最小とは限らない。
- モデル：消去を逆にたどって値を決め（各変数は範囲内で 0 に最も近い値）、最後に元の式で評価する。満たさなければ `Unknown`。
- 算術はすべて検査付きで、64bit をはみ出たら `Unknown("overflow")`。
- 予算：理論の手数 20000、DPLL の節点 2000、1 問題の制約数 500（`smt_max_*`）。

`kek smt <file>`（隠しコマンド、`compiler/smt_main.kek`）が問題を解く。問題は空行で区切り、`//` の行はコメント：

```
problem index_safe           // 省略可。結果の前に表示する
hyp lo: 0 <= i < n           // 名前付きの仮定（unsat core に現れる）
hyp: n == len(v)             // 名前なしの仮定
goal: i < len(v)             // 省略すると false
```

式は線形項（`+ -`、定数との `*`、括弧）の比較 `<= < >= > == !=`（`a <= b < c` は連言）と `&& || ! true false`。atom は英数字と `_ # . [ ]` の名前で、括弧の引数を続けてよい（`len(v)`、`v.len()`、`x#2`）。出力は 1 問題 1 行：`valid [lo, n]`、`invalid i=0 n=0`、`unknown: overflow`。`tests/run.sh smt` は `testdata/smt/*.smt` を `.out` と突き合わせ、`kek smt -random` で乱数の式を [-4, 4]^3 の総当たりと比べ（判定・core・モデル）、`kek smt -bench 10000` の時間を測る。

## `kek` との統合

- `kek check`：篩型の違反は型エラー（`phase: "refine"`）、overflow・division は `phase: "lint"` の警告（設定でエラー）。（実装済み）
- `kek assure`：関数ごとに `refine.no_overflow`・`refine.no_div_zero`・`refine.index_safe` を保証として記録する（証明できた関数だけ。根拠は `smt`）。（実装済み）
- `kek test`：引数が篩型の別名のプロパティテストは、述語を満たす値だけを生成する（棄却法から始める）。（実装済み）
- `kek mutate`：篩型の証明に失敗する変異体は「篩型で検出」として数える。（実装済み）
- Lean：コア計算に事前条件付きの呼び出し・除算・固定長配列の添字を加え、「検証条件が成り立つプログラムは除算と添字で行き詰まらない」を証明する（ソルバは仮定として与える）。

## 篩型（実装済み）

実装は `compiler/refine.kek`（prefix `ref_`）。構文は `compiler/parse.kek`（`where` の述語は `AstFuncDecl.preds`、別名は `AstFile.aliases`、`v[i]` は `AstExpr::Index`）、型検査は `compiler/chk_check.kek`、整形は `compiler/fmt_print.kek`。言語としての説明は [language.md](language.md)、`kek check -json` の形式は [tooling.md](tooling.md)。

| 項目 | 決定 |
| --- | --- |
| `where` の項目 | trait の境界か述語かは字句で決める：先頭から型に現れる字句（識別子、`::`、`<>`、`()`、`&`、`mut`、`fn`、`->`）だけが続いて括弧の外の `:` に達すれば境界、それ以外は述語。整形では境界を先に、述語をその後に 1 行ずつ書く（同じ行の後ろのコメントは残す） |
| 述語の型検査 | 引数と `result`（戻り値の型）を束縛した `Bool` の式として検査し、線形の断片から外れるもの（変数どうしの `*`、呼び出し、`/`・`%`、整数以外の比較、`mut` のフィールド）はエラー。事後条件のある関数は `result` という名前の引数を持てない。別名の述語は `self`（別名の中身の型）で検査する |
| 型の別名 | 透過的（`chk_resolve_type` で中身の型に置き換える）。名前は AST に残り、篩型の検査は AST の型の綴りで別名を見つける。別名の別名は述語を重ねる（`type Small = Index where self < 10` は `0 <= self && self < 10`）。循環はエラー。総称的な別名（`type A<T>`）はない |
| `v[i]` | `Vec` だけ（`String` は UTF-16 の添字と文字の型の扱いが決まっていないので `get`/`char_at` のまま）。代入 `v[i] = x` はない（`set` を使う）。型は要素の型 |
| `v[i]` のコード | 既存の IR だけに下ろす：`vec.len`、`lt`/`le` の比較 2 つで `unreachable` に分岐、`vec.at`。証明があるので罠は実行されないはずだが、wasm の `array.get` は容量までしか検査しないので残す（比較 2 回）。IR に新しい命令はなく、Lean の参照インタプリタ（`vec.at` は範囲外で `trap`）と差分テストは変更不要 |
| 対象の関数 | 利用者の関数だけ（core と prelude は検査しない）。型エラーがあるときは走らない。型検査器が「篩型を使う関数」を記録する（`v[i]`、述語のある別名の解決、篩型のフィールドを持つ struct の構築・代入）。契約（述語、篩型の引数・戻り値）を持つ関数、それを呼ぶ関数、記録された関数だけを調べ、ほかは何もせずに飛ばす |
| 歩き方 | 関数ごとに 1 回、記号的に歩く。`Int` の変数は atom（代入ごとに `x`、`x#1`、…）、`Vec`・`String` の長さも atom（`len(v)`）。`Bool` の不変な変数は式のまま。線形でない `Int` の式（呼び出し、`/` など）は新しい atom。定数での `/`・`%` は商の範囲（0 方向への切り捨て）を事実にする（二分探索の `lo + (hi - lo) / 2` が `lo <= mid < hi` になる） |
| 合流 | `if`・`match` の後、枝で値の変わる変数は新しい版になり、枝の事実は 1 つの選言になる（`(c && x#2 == ...) \|\| (!c && x#2 == ...)`）。値を返す `if`・`match` も同じ（`clamp` の事後条件が証明できる）。`return`・`break`・`continue` で終わる枝は合流に入らない（`if v.len() == 0 { return 0; }` の後は `v.len() != 0`） |
| ループ | 本体で変わりうる変数と長さ（事前の走査で求める）をループの先頭で新しい版にする。候補の不変条件（各変数の「入口の値以上／以下」と、入口で成り立つ `x <= y`（`x` は変わる変数、`y` は変数か長さ））を仮定して本体を歩き（診断は出さない）、代入のたびに候補をソルバで確かめ、壊れた候補を捨てて繰り返す（Houdini、最大 3 回）。安定した候補だけを事実として本体を本番で歩く。`while` の後は先頭の状態と条件の否定（本体に自分の `break` がなければ） |
| 長さの忘却 | `push`（長さ +1）、`pop`（0 なら 0、そうでなければ 1 減る）。非原始的な引数（`Int`・`Bool`・`String`・capability 以外）を渡す利用者の関数・クロージャの呼び出しは、`Vec` の長さをすべて忘れる（グローバル変数がないので、届くのは引数からだけ）。`Vec::new()` で作り、レシーバ・添字の基・`for` の対象・`return` の値としてしか使わない局所の `Vec`（孤立した Vec）は別名を持たないので、ほかの `Vec` への操作で忘れず、それへの `push` もほかを忘れさせない（`out.push(v[i])` のループで `v.len()` が残る）。core の関数は長さを変えないものとして扱う。クロージャの本体は作られた時点の事実で、長さを忘れてから歩く |
| VC の解き方 | 目標と atom を共有する事実だけを（推移的に）集めてソルバに渡す。`Invalid` は反例（目標の atom の値、ソースの名前に戻す：`i = 0, v.len() = 0`）、`Unknown` は「unknown (solver budget: 理由)」。`kek check -v` と `-json` は、ソルバに渡した事実（証明できたものは unsat core）を位置付きで示す |
| 長さの上限 | `0 <= len(v) <= 2^32 - 1`（実行時の長さは 32bit）。`i < v.len()` の変数の `i + 1` がはみ出さないことが示せる |
| オーバーフローの検証条件 | ソルバは 64bit の係数で計算するので、`2^63` の境界を否定・結合するとすぐにはみ出して `Unknown` になる。そこで `\|結果\| <= 2^60` を示す（64bit に収まることより強いので健全。8 倍までの係数の結合なら係数がはみ出さない）。反例は `2^60` を超える値になる。変数どうしの `*` は示せない（lint） |
| `MIN / -1` | `/` の 2 つ目の検証条件は除数が 0 でないと示せたときだけ出す（1 つの除算に診断は 1 つ）。`x == MIN` は `-x + MIN == 0` と書くが、ソルバがモデルを評価するときに `-MIN` がはみ出すので、反例は作れず unknown になる |
| 既定の範囲（`"auto"`） | オーバーフローと除算の検証条件は、既定では篩型を使う関数（契約がある、`v[i]` がある、契約のある関数を呼ぶ、篩型の別名を使う）だけで出し、警告にする。`"lint"`・`"error"` は全関数（警告／エラー）、`"off"` は出さない。セルフホストのコンパイラ（篩型を使わない）は既定で何も検査せず `./kek check compiler` は警告なしのまま。`overflow = "lint"` にするとコンパイラでも数千の警告になる（ほとんどの `i + 1` の上限は事実から分からない）ので既定にしない |
| 設定の読み込み | `kek check`（`-json`・`-v` も、常駐コンパイラ経由も）と `kek build`・`kek run` が `./kekkai.toml` の `[refine]` を読む。値の誤りはエラー。`kek test`・`kek mutate` は既定の設定で検査する |
| 健全性の前提 | 推論は数学的な整数で行うので、オーバーフローの検証条件を証明していない関数では、事実（`x == e`）が実行時のラップアラウンドで崩れうる。`kek assure` の `refine.index_safe` などは、同じ関数の `refine.no_overflow` と合わせて読む。ソルバの正しさは公理扱い |
| `kek assure` | 全関数で全種類の検証条件を出して（`all`）、種類ごとに 1 つ以上あり全部証明できた関数に `refine.index_safe`・`refine.no_div_zero`・`refine.no_overflow`（根拠 `smt`）を記録する。検証条件のない種類は記録しない（台帳を増やさない）。失うと弱化 |
| `kek test` | 篩型の別名の引数は、述語を満たすまで最大 1000 回生成し直す（`Int` の別名は、述語が直接書く定数の上下限 `self op k` の範囲から交互に引く）。満たせなかったケースは飛ばす。縮小の候補も述語を満たすものだけ |
| `kek mutate` | 篩型を使う関数の変異体は「型が付かないかもしれない」ものとして、平のコードで 1 つずつ（まとめて二分探索で）検査し、篩型の証明に失敗したものは「型で検出」になる。変異体をまとめた schema は prelude の関数呼び出しで値を隠すので、篩型の検査をしない |
| 性能 | 篩型を使わない関数は型検査器の記録を見るだけで飛ばす。`./kek build-timings compiler` の check は同じ入力で変更前とほぼ同じ（下記） |

テスト：`testdata/check/err_refine_*.kek`（範囲外の添字、呼び出しでの事前条件、事後条件、別名、設定でエラーにしたゼロ除算、ループの off-by-one、述語の構文と型）、`testdata/run/refine.kek`（`for` の範囲、`while lo < hi` の二分探索、`clamp` の事後条件）、`tests/suites/refine.sh`（`-v`・`-json` の反例と事実、`[refine]` の設定、`tests/refine/golden`）、`tests/fmt/refine.*`、`tests/suites/assure.sh` の `refine`、`tests/suites/pbt.sh` の `refined`、`tests/suites/mutate.sh` の `refine`。

未実装：`checked_add` などの `Option` を返す演算、`String` の `s[i]`、`v[i] = x`、等式の不変条件（`out.len() + i == v.len()` のような関係はループで失われる）、述語の中の `/`・`%`、総称的な別名、`match` の束縛での struct のフィールドの事実、篩型の struct フィールドの値を `kek test` が生成すること、反例の `MIN / -1`。

## 個人情報の最小版（`Pii<T>`）（実装済み）

- core の型 `Pii<T>`：`Pii::new(x)` で包む。`to_string`・連結・`==` 以外の比較・`Hash` はない（`String` として取り出せない）。
- 格下げ：`mask() -> String`（`String` の場合、先頭 1 文字と `@` 以降を残す）、`hash() -> String`（固定のハッシュ）、`expose_unchecked() -> T`（脱出口）。格下げの呼び出しは `kek caps` に一覧され、`kek assure` に前提（`pii.declassify`）として記録され、`[pii] max_declassify_per_module` で上限を設けられる。
- `#[derive]` は `Pii` のフィールドを持つ struct に `PartialEq`・`Eq` 以外を導出しない。

実装（`lib/core/pii.kek`、`compiler/chk_pii.kek`。言語としての説明は [language.md](language.md)）での決定：

| 項目 | 決定 |
| --- | --- |
| 不透明さ | 新しい仕組みは足さない。フィールド名 `__value` は core と prelude の外では字句解析が拒否する（`__` は予約）ので、読むことも struct リテラルで作ることもできない。IR も Lean の参照インタプリタも変えない（ふつうの core の Kekkai） |
| `mask` | 最後の `@` 以降（e-mail のドメイン）と先頭の 1 コードポイントを残し、ほかのコードポイントを 1 つずつ `*` にする（長さは見える）。`@` がなければ先頭以外をすべて隠す。`@` の前が 1 文字ならそれも隠す（`"a@x.com"` → `"*@x.com"`、`"x"` → `"*"`）。`""` → `""` |
| `hash` | `T: Hash` の `Pii<T>` に定義。core の `DefaultHasher`（固定、seed なし）の結果を 16 桁の小文字の16進数（2 の補数）で |
| `Hash` trait | 実装しない。`HashMap` のキーには `Ord` も要るが、`Ord` は `Pii::new(候補)` との比較による二分探索で値を割り出せるので実装しない。`Ord` なしでは `Hash` はキーの役に立たず、`h.finish()` で記録されない格下げになるだけ。キーには `p.hash()` を使う |
| derive | `Pii` を含む型（フィールドの型のどこかに `Pii`）には `PartialEq`・`Eq` だけ。ほかは「personal data (`Pii`), which has no `Hash`」のエラー（設計の `Hash`・`PartialEq` から `Hash` を外し `Eq` を加えた） |
| エラーの説明 | `Pii` が `String` などとして使われた型エラー（引数、連結、比較、メソッドがない）に「`mask()`・`hash()`・`expose_unchecked()` で格下げする」を添える |
| 格下げの承認 | 関数の属性 `#[declassify(reason = "...", owner = "...", expires = "YYYY-MM-DD")]` に書く（`#[allow(similar)]` と同じくコード側の前提）。`[pii] declassify_requires` は、格下げする関数のこの属性に必須の項目。期限切れは `kek assure` の expired になる |
| モジュール | `max_declassify_per_module` のモジュールはファイル |
| 残る制限 | `==` は使えるので、候補を包んで比べる総当たりは防げない。格下げした値の行き先は追わない（本格版の情報フロー型、P2） |

## 冪等性（`#[handler(idempotent)]`）（実装済み）

- `#[handler(idempotent)]` のハンドラ（と、そこから呼ばれる関数）は、冪等でない capability の操作を呼べない：`Net.post`、`tx.outbox`、キーを指定しない書き込みなど。`Net.get`・`Db.get`・`tx.put`（同じ値の上書き）は冪等とみなす。
- 冪等性は capability と同じく呼び出しグラフを通じて検査し、`kek caps` と `kek assure`（`idempotent`）に現れる。

実装（`compiler/chk_pii.kek`）での決定：

| 項目 | 決定 |
| --- | --- |
| 冪等な操作 | 許可リスト：`log.*`、`clock.now_ms`、`net.get`、`db.get`、`db.transaction`、`tx.get`・`tx.put`・`tx.delete`・`tx.commit`・`tx.rollback`、`fs.read`・`fs.list`・`fs.set_cwd`。リストにない操作（今後増えるものを含む）は冪等でない |
| 冪等でない操作 | `net.post`、`tx.outbox`、`fs.write`・`fs.write_bytes`（ストアの外の書き込みで、ハンドラからは保守的に禁止）、`fs.read_line`（入力を消費する）、`random.int`（リトライで別の ID・トークンを作り、それを書けば 2 件目のレコードになる） |
| `tx.delete` | 冪等（2 回目は何もしない） |
| `Clock` | 冪等（時刻は読むだけ。キーを時刻から作るような値の依存は見ない） |
| 判定の単位 | 操作の種類だけ。値（何を書くか、キーの有無で分岐するか）は見ない。Idempotency-Key で重複を検出して `tx.outbox` を使うパターン（`examples/payments`）は型では冪等と認めない |
| 呼び出しグラフ | 型検査器の `calls`（関数・メソッド・関数値）を幅優先でたどり、最短の経路で報告する：``idempotent handler `h` reaches `net.post` via `settle` -> `charge` (payments.kek:12), which is not idempotent: a retry would do it again``。trait のメソッド呼び出しはたどらない（capability を取る trait のメソッドは今はコンパイルできない） |
| `kek caps` | JSON は全関数に `idempotent`（純粋な関数は true）。テキストは capability を受け取る関数に `idempotent: true` か `idempotent: false (<op> via <callee>)`、ハンドラに `#[handler(idempotent)]` |
| `kek assure` | 真偽の保証 `idempotent`（capability を受け取る関数で、成り立つときだけ記録）。成り立たなくなると弱化。純粋になって消えたときは変化にしない（`effects` の強化が出る） |
| Workers | 実行時の変更はない。リトライの設定（Queues・Workflows の再試行など）に使うなら `kek caps -json` の `idempotent` を参照する。`worker.js`・`wrangler.toml` にはまだ出さない |
