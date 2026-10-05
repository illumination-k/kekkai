# Kekkai コア計算の Lean 形式化

Kekkai の P0 性質（capability 渡しによる副作用の制御と、線形なトランザクション）、P1 の篩型（事前条件・事後条件と、ゼロ除算・添字の範囲外・オーバーフローの排除）、P2 の情報フロー（`Labeled<L, T>` と非干渉性）を、小さなコア計算として Lean 4 で形式化し、証明したものです。設計ドキュメント（`docs/design.md`）の「検証戦略」とロードマップ 1〜3 段階、`docs/refinement.md` の「Lean」の項に対応します。

- Lean 4.34.1、依存なし（Mathlib なし、core のみ）
- `sorry` / 独自 `axiom` / `native_decide` なし。主要定理が依存する公理は Lean 標準の `propext`, `Quot.sound`, `Classical.choice` だけ
- ビルド: `cd lean && lake build`（またはリポジトリ直下で `mise run lean`）

## ファイル構成

| ファイル | 内容 |
| --- | --- |
| `Kekkai/Syntax.lean` | 型・capability 種別・値・式・述語（`Atom`, `Term`, `Pred`）・関数定義（事前条件・事後条件つき）・プログラム |
| `Kekkai/Basic.lean` | リスト補題（`Forall2`, `lookupAll`） |
| `Kekkai/Typing.lean` | 型付け規則 `HasType`（線形 `Tx` の状態を受け渡す）、`WTProg` |
| `Kekkai/Semantics.lean` | fuel つき big-step 参照インタプリタ `eval`（トレースを出力）。算術 `arith`（ゼロ除算・オーバーフローは `fault`） |
| `Kekkai/Safety.lean` | 定理 1: 型安全性 |
| `Kekkai/Effects.lean` | 定理 2: エフェクト健全性、純粋関数の系 |
| `Kekkai/Monitor.lean` | トランザクション規律を検査するトレースモニタとその性質 |
| `Kekkai/Linearity.lean` | 定理 3・4: `Tx` の線形性、トランザクション内で取り消せない副作用を禁止 |
| `Kekkai/NoLeak.lean` | 定理 5: capability の非漏洩 |
| `Kekkai/Pred.lean` | 述語の意味（整数環境 `IEnv`）、意味論的な含意 `Entails`、de Bruijn の付け替え、実行時の環境から整数環境への写像 `toI` |
| `Kekkai/Refine.lean` | 篩型の層: 検証条件の判断 `Ref`、`WTRefFun`/`WTRefProg`、定理 6〜8 |
| `Kekkai/Flow.lean` | 情報フロー: 低等価 `LowEq`・`OutLow`、定理 9（非干渉性） |
| `Kekkai/Examples.lean` | 例: 型付けの導出、インタプリタの実行結果（`rfl`）、型エラーになるプログラム。篩型: 添字の範囲を検査した配列の読み出しとその呼び出し元、`if d != 0` で守った除算、事後条件とオーバーフロー、検証条件が成り立たず拒否されるプログラム。情報フロー: `Labeled` の `map` に相当する計算とその実行、非干渉性の適用例、ラベルつきの値での分岐・ログ出力・`lbind` の中からの暗黙のフローが型エラーになること |
| `Kekkai/IR/*.lean`, `KekkaiRef.lean` | Kekkai IR の実行可能な参照インタプリタ `kekkai-ref`（下記） |

## コア計算

### 構文

- 値の型 `Ty`: `unit | bool | int | arr | lab τ`（`arr` は整数の固定長配列、`lab τ` はラベルつきの値 `Labeled<L, τ>`）。**capability 型は値の型に含めない**（第二級）。`lab _` 以外の型を公開型（`Ty.pub`）と呼ぶ
- 値 `Val` には、ラベルつきの値 `lab v` と、中で `abort` したラベルつきの計算の結果 `labErr` がある
- capability 種別 `CapKind`: `log`, `net`（取り消せない副作用）, `db`（抽象的なトランザクショナルストア）, `tx`（線形）
- 値の変数と capability の変数は、別々の de Bruijn 文脈 `Γ`, `Δ` に置く
- 式 `Expr`:
  - `val`, `var`, `let_`, `ite`, `bin`（整数の `+ - * / % < <= == !=`）
  - `len a`（配列変数 `a` の長さ）、`index a i`（`a[i]`。添字は A 正規形で変数）
  - `call f args caps`: トップレベル関数の呼び出し。引数は A 正規形（値変数と capability 変数の並び）
  - `log c e`, `fetch c e`
  - `transaction d body`: `d : Db` からトランザクションを開始する。`body` の中では capability 変数 0 が新しい `tx` になる
  - `store op c args`: 開いているトランザクションへの、消費しないストア操作。`op ∈ {get, put, delete, outbox}`
  - `commit c`: `Bool` を返す（`true` は成功、`false` は競合などによる失敗）。**どちらの場合もトランザクションは終了する**
  - `rollback c`
  - `abort`: 早期脱出（`?` で `Err` を受けたときに相当）。生きているトランザクションの中では自動で rollback する
  - `wrap e`: `Labeled::new(e)`。`e : τ` なら `lab τ`
  - `lbind e body`: `e : lab τ` の中身を値変数 0 に束縛して `body : lab τ'` を計算する（`and_then`）。`map` は `lbind e (wrap …)`、`zip` は `lbind` の入れ子
- プログラムはトップレベルの一階関数の列。`FunDef` は値引数・capability 引数・返り値の型・本体と、篩型の契約 `pre`・`post`（既定は `tt`）からなる。capability 引数を持たない関数は純粋

`Db` は SQL に限らない抽象的なトランザクショナルストアです（実体は Durable Objects storage、D1、分散 KV、インメモリなどで、ランタイムアダプタが選ぶ）。ストア操作の応答（`get` の値）と commit の成否は `Oracle` が与え、どの定理も任意の oracle について成り立ちます。

### 型付け `HasType P Γ Δ s e τ s'`

- `Δ : List (Option CapKind)`。`none` は「マスクされた」スロットで、de Bruijn 番号を保ったまま使えなくする
- `s, s' : TxSt = none | live | done` は、最も内側の `transaction` ブロックが所有する `tx` の状態で、評価順に受け渡される（`Δ ⊢ e ⊣ Δ'` 型の線形文脈の受け渡し）
  - `commit` / `rollback` は `live → done`。したがって各経路で高々 1 回
  - `transaction` の本体は `live → done` で型付けする。したがって正常終了する経路では少なくとも 1 回
  - `store` と、関数への `tx` の受け渡し（借用）は `done` でないことを要求する。関数本体は `none` で検査するので、呼び出された側は `tx` を消費できない
  - `abort` は `live → done` を許す（自動 rollback）
- `transaction` の本体は `some tx :: mask Δ` で型付けする。`mask` は `Log` 以外（`Net`, `Db`, 外側の `Tx`）を隠すので、トランザクション内では `Net` を直接にも、関数呼び出し経由でも使えない。トランザクションの入れ子も起こらない
- `len a`・`index a i` は `a : arr`（と `i : int`）を要求し `int` を返す。添字の範囲は型ではなく篩型の層が検査する
- **ラベルつきの値**: `ite`・`bin`・`len`・`index` は公開型（`bool`, `int`, `arr`）を要求するので、`lab τ` の値では分岐も計算もできない。`log` のメッセージ、`fetch` の引数、`store` の引数は公開型でなければならない（ラベルつきの値は出力に渡せない）。`lbind` の本体は**空の capability 文脈** `[]` と状態 `none` で型付けする。したがって本体は capability を一切使えず（capability 引数を持つ関数も呼べない）、トランザクションも持たない純粋な計算になる。ラベルつきの計算の「pc」はラベルそのもので、その中の分岐は外から観測できない
- 関数が well-typed であるとは `HasType P fd.params (fd.caps.map some) .none fd.body fd.ret .none` が成り立つこと

### 意味論 `eval O P fuel env ρ σ e : Result`

- `Result = timeout | stuck | fault (f : Fault) | done (o : Outcome) (σ : St) (tr : List Event)`。燃料切れ（`timeout`）、動的型エラー（`stuck`）、実行時の誤り（`fault`）を区別する
- `Fault = divByZero | outOfBounds | overflow`。整数は数学的な整数で、`+ - *` と `/` の結果が `[-2^63, 2^63)` を外れると `overflow`（`/` では `MIN / -1` だけ）、`/`・`%` の除数が 0 なら `divByZero`、`a[i]` で `i < 0` か `a.len() <= i` なら `outOfBounds`。`/` は 0 方向への切り捨て（`Int.tdiv`）、`%` は被除数の符号（`Int.tmod`）。**コンパイルされたコード（`x / 0 == 0`、ラップアラウンド）とはわざと違えてある**。篩型が排除するものを `fault` として観測できるようにするためで、検証条件が成り立つプログラムではこの違いは現れない（定理 6）
- 実行時の capability は識別子にすぎない。外から渡された資源は `RCap.res id`、`transaction` が作るハンドルは `RCap.tx t`（`t` はカウンタから取る新しい ID）
- イベント `Event`: `log c v | fetch c v | txBegin db t | txOp t op args | txCommit t | txRollback t`
  - 失敗した commit、明示的な rollback、`abort` による自動 rollback は、どれも `txRollback t` になる
- インタプリタは防御的に作ってある。閉じたトランザクションへの操作や、`tx` を消費せずに本体が正常終了することは `stuck` になる
- `runFun O P n f vs caps` はエントリ関数を `caps.map .res` と初期状態で実行する
- `lbind e body` は `e` の値が `lab a` なら、`body` を `a :: env` と**空の capability 環境** `[]` で評価する。`body` の中の `abort`（呼び出した関数の中のものも含む）は `lbind` の外に出ず、結果は `labErr` になる（`lbindResult`）。`e` の値が `labErr` なら `body` を実行せず `labErr` を返す

### 篩型の層 `Ref P Φ e Ψ`（`Kekkai/Refine.lean`）

- 述語 `Pred`: 原子 `Atom = var i | len i`（値変数 `i` の整数値（`Bool` は 0/1）と、配列変数 `i` の長さ）の上の整数の項（`const`, `+`, `-`, `*`）、比較（`<=`, `<`, `==`）、`not`/`and`/`or`。コンパイラの `SmtF`（`compiler/smt.kek`）と同じ言語で、ソルバが決定するのは線形の部分（定数との `*`）
- 意味は整数環境 `IEnv = Atom → Int` で与える。**`Entails Φ p` は意味論的な含意**（`Φ` をすべて満たすどの整数環境でも `p` が成り立つ）。証明はソルバのコードを一切信用せず、「ソルバが `smt_prove(Φ, p)` に `Valid` と答えたなら `Entails Φ p`」だけが信頼する仮定になる（ソルバの正しさは公理扱い、`docs/design.md`）
- `Ref P Φ e Ψ`: 事実 `Φ`（`e` の文脈の上の述語）のもとで `e` の検証条件がすべて成り立ち、`e` が値 `r` を返したら `Ψ`（`r :: 文脈` の上の述語。番号 0 が結果）が成り立つ。途中の結果についての事実は含意で消去する（`conseq`、`ite`・`bin` の前提）ので、事実はいつも現在の文脈の述語になる。変数の束縛は de Bruijn 番号のずらし（`Pred.shift`, `Pred.up1`）で扱う
- 関数の契約 `WTRefFun P fd`: `pre` は引数だけ、`post` は結果と引数だけに言及し（`Pred.wf`）、`Ref P [fd.pre] fd.body fd.post`。`WTRefProg P` はすべての関数がそうであること
- `wrap`・`lbind` の結果については何も分からない（`.tt`）。`lbind` の本体の検証条件は、外側の事実（`Φ.shift`）だけのもとで、中身 `x` のどんな値についても成り立たなければならない
- `HasType` とは独立した判断で、型付けの規則は変えていない。両方を仮定すると「`stuck` にも `fault` にもならない」（`refined_safety`）

## 表面言語との対応

| 表面言語 | コア |
| --- | --- |
| `fn f(x: Int, db: &Db, log: &Log)` | `FunDef`（`params = [int]`, `caps = [db, log]`） |
| `&Cap` は保存も返却もできない | capability は値の型を持たず、`let` で束縛できず、返り値にもならない（`Δ` は別の文脈） |
| `db.transaction(\|tx\| { ... })` | `transaction d body` |
| `tx.get(k)`, `tx.put(k, v)`, `tx.delete(k)`, `tx.outbox(url, body)` | `store op c args` |
| `tx.commit()`（失敗しうる） | `commit c : Bool`（成否にかかわらず `tx` を消費する） |
| `tx.rollback()` | `rollback c` |
| `?` による早期脱出と自動 rollback | `abort` |
| `f(a + 1, b)` | `let` で A 正規形にしてから `call` |
| `fn f(v: Vec<Int>, i: Int) -> Int where 0 <= i, i < v.len(), lo <= result` | `FunDef`（`pre`, `post`。`result` は `post` の番号 0） |
| `v[i]`, `v.len()` | `index a i`, `len a`（配列は不変・固定長の `Val.arr`） |
| `Labeled<PII, T>` | `lab τ`（ラベルは 1 つの秘密レベルにまとめる） |
| `Labeled::new(x)`, `PII::label(x)` | `wrap e` |
| `l.and_then(\|x\| ...)` | `lbind l body`（本体は空の capability 文脈で純粋） |
| `l.map(\|x\| e)` | `lbind l (wrap e)` |
| `l.zip(&m, \|x, y\| e)` | `lbind l (lbind m↑ (wrap e))`（`m↑` は 1 つずらした変数） |
| `Labeled` は表示・比較・ハッシュできず、出力に渡せない | `ite`・`bin` などは公開型を要求し、`log`・`fetch`・`store` の引数は公開型 |
| 閉包の中の `?` | `lbind` の本体の `abort` は外に出ず `labErr` になる |
| 宣言的な秘匿解除 `mask`・`hash`・`expose_unchecked` | コアに含めない（使うプログラムは定理 9 の保証の外。`kek caps`・`kek assure` が列挙する） |

## 定理一覧

| # | 性質 | Lean の名前 | 内容 |
| --- | --- | --- | --- |
| 1 | 型安全性 | `Kekkai.type_safety`（コア補題 `Kekkai.eval_safe`） | well-typed なプログラムのエントリ関数（`Tx` 引数を取らない）を、型の合う引数と capability で実行すると、どんな fuel でも `stuck` にならない。値を返したときは、その値が宣言された返り値の型を持つ |
| 2 | エフェクト健全性 | `Kekkai.effect_soundness`（コア補題 `Kekkai.eval_effects`） | トレース中のすべてのイベントについて、使った資源 capability は渡された `caps` の中にあり、トランザクション ID はその実行中に作られたものである。型付けを仮定せず、意味論だけから成り立つ |
| 2' | 純粋性 | `Kekkai.pure_call_no_effects`, `Kekkai.eval_no_caps` | capability 引数を持たない関数を well-typed に呼び出すと、トレースは空になる |
| 3 | `Tx` の線形性 | `Kekkai.tx_linearity`（`Kekkai.run_txsafe`, `Kekkai.TxSafe.linear`） | トレース中の各 `txBegin _ t` の後には、`t` を終了するイベント（`txCommit t` / `txRollback t`）がちょうど 1 つ現れる。その後 `t` は二度と現れない（ストア操作も、2 回目の commit も起こらない） |
| 4 | トランザクション内で取り消せない副作用を禁止 | `Kekkai.no_irrevocable_in_tx`（`Kekkai.TxSafe.no_irrevocable_inside`） | `txBegin _ t` から `t` の終了までの間に `fetch` は現れない |
| 5 | capability の非漏洩 | `Kekkai.result_independent_of_caps`（コア補題 `Kekkai.eval_rename`）、`Kekkai.no_expr_has_cap_type`, `Kekkai.no_fun_returns_cap`, `Kekkai.Val.capIds_nil` | 型の上では、どの式にもどの関数の返り値にも capability 型は付かない。値の上では、`Val` が capability を含まない（構成上自明）。意味論の上では、渡す capability の ID を任意に付け替えても、結果（返り値・最終状態）は変わらず、トレースの ID だけが付け替わる。つまり結果は capability に依存すらしない |
| 6 | 篩型の安全性 | `Kekkai.refinement_safety`（コア補題 `Kekkai.eval_refine`）、個別に `Kekkai.no_div_by_zero`, `Kekkai.index_safe`, `Kekkai.no_overflow` | すべての関数が契約を満たす（`WTRefProg P`）なら、事前条件を満たす引数でどの関数を実行しても、どんな fuel・oracle・capability でも `fault`（ゼロ除算・添字の範囲外・オーバーフロー）にならない。型付けは仮定しない |
| 7 | 事後条件 | `Kekkai.postcondition_holds` | 同じ仮定のもとで、返った値は事後条件を満たす（`fd.post.holds (toI (v :: vs))`） |
| 8 | 型安全性 + 篩型の安全性 | `Kekkai.refined_safety` | `WTProg P` と `WTRefProg P` のもとで、型の合う引数が事前条件を満たせば、`stuck` にも `fault` にもならず、返った値は宣言された型を持ち事後条件を満たす |
| 9 | 非干渉性（停止性を区別しない） | `Kekkai.noninterference`（コア補題 `Kekkai.eval_ni`）、系 `Kekkai.noninterference_public` | well-typed なプログラムのエントリ関数を、同じ oracle・同じ capability で、ラベルつきでない引数がすべて一致する（`PubAgree`）型の合う 2 組の引数で実行する。**両方が終了したなら**（fuel はそれぞれ任意）、トレース（ログ・fetch・トランザクションとストア操作・commit/rollback のすべて）と最終状態は等しく、結果は低等価（`OutLow`: どちらも値を返し、その値はラベルつきの部分を除いて等しいか、どちらも `abort`）。返り値の型が公開型なら結果そのものが等しい（`noninterference_public`） |

定理 1〜5 の主張は P1 の拡張の前と同じです（`type_safety` の「`stuck` にならない」は、`fault` とは区別されるのでそのまま成り立ちます）。P2 の拡張では `log`・`fetch`・`store` の型付け規則に「引数が公開型」という前提を足し、`wrap`・`lbind` の規則を足しました。定理 1〜8 の主張は変わらず、証明に新しい構文の場合を足しています。

### 非干渉性（定理 9）

- 低等価 `LowEq v w`: `v = w`、または `v` も `w` もラベルつき（`lab _` か `labErr`）。観測者はラベルつきの値の中身以外をすべて見られる
- 鍵になる補題（`labRes_body`）: `lbind` の本体は空の capability 文脈で型付けされるので、イベントを出さず状態も変えず（`eval_no_caps`）、結果は必ずラベルつき（`abort` しても `labErr`）。したがって秘密の値で本体の中の分岐や計算が変わっても、外からは区別できない
- `eval_ni` は fuel について帰納法で、2 つの実行が同じ分岐をたどることを示す。分岐（`ite`）や算術・添字・出力に使われる値は公開型なので、低等価なら等しい
- **停止性を区別しない**（TINI）: どちらかの実行が `timeout` や `fault` になる場合は主張の外。ラベルつきの計算の中のループや `fault`（ゼロ除算など）は秘密に依存しうる観測可能な停止のチャネルで、この定式化では除外する。`fault` は篩型の層（定理 6）を通ったプログラムでは起こらない
- 秘匿解除（`mask`, `hash`, `expose_unchecked`）はコアに含めない。保証されるのは「ラベルに従って流れたこと」で、秘匿解除を使うプログラムはこの定理の外にある

定理 3・4 は、トレースモニタ `TxSafe`（`Kekkai/Monitor.lean`）を経由して証明しています。モニタは「開いているトランザクションは高々 1 つ」「`txBegin` の ID は未使用のもの」「`txOp`, `txCommit`, `txRollback` は開いているトランザクションに対してだけ」「`fetch` はトランザクションが開いていないときだけ」「最後には何も開いていない」を検査します。`run_txsafe` は、well-typed なプログラムのあらゆる実行（値で終わっても `abort` で終わっても）のトレースがモニタに受理されることを示します。

### 検証条件と事実（`docs/refinement.md`）との対応

| コンパイラの検査 | Lean |
| --- | --- |
| 呼び出し: 呼び出し先の事前条件（引数で置き換えたもの） | `Ref.call` の前提 `Entails Φ (fd.pre.substArgs args)` |
| `return`・本体の末尾: 事後条件 | `WTRefFun` の `Ref P [fd.pre] fd.body fd.post`（`conseq` で結果の事実から導く） |
| `v[i]`: `0 <= i && i < v.len()` | `Ref.index` の前提 |
| `/`・`%`: 除数 `!= 0` | `opVC .div`, `opVC .mod`（`/` はさらに `MIN / -1` でないこと） |
| `+`・`-`・`*`: 結果が `[-2^63, 2^63)` に収まる | `opVC .add`, `.sub`, `.mul`（`inI64`） |
| 事実: 事前条件、`v.len() >= 0` | `WTRefFun` の `[fd.pre]`、`Ref.lenNonneg` |
| 事実: `if` の条件（否定は `else` 側） | `Ref.ite` の `pT`・`pF`（条件の結果が 1 / 0 であることから含意で導く） |
| 事実: 不変な `let x = e` の `x == e` | `Ref.let_`（`e` の結果の事実。線形な `e` では `opFact`, `valFact`, `Ref.var`, `Ref.len` から `x == e` が出る） |
| 事実: 呼び出しの結果は事後条件を満たす | `Ref.call` の結果 `fd.post.substRes args` |
| ソルバ（Omega test）の正しさ（公理扱い） | `Entails` を意味論的に定義し、仮定として前提に置く（Lean はソルバを含まない） |
| `kek assure` の `refine.no_div_zero`・`refine.index_safe`・`refine.no_overflow` | `no_div_by_zero`, `index_safe`, `no_overflow` |

コアに入れていないもの: 篩型の別名（`type Port = Int where ...`）、`while`・`for`・`match`・可変変数（ループで忘れる事実、`for` の範囲の事実、代入ごとの版）、`&&`・`||` の短絡（コアに真偽値の演算子がない）、`v.len()` の事実を変更操作の後で忘れること（コアの配列は不変）、不変なフィールド `p.x`、反例と unsat core の報告、`lint` と `error` の区別（コアでは検証条件はすべて必須）。

## 実際の言語からの簡略化

- 値は `Unit`, `Bool`, `Int` と整数の不変な配列だけ。`Result` 型や和型、レコード、篩型の別名はない。`abort` はエラー値を持たず、関数境界を越えて最上位まで伝播する（捕捉する構文はない）
- 関数は一階で、呼び出しは A 正規形
- 同時に開けるトランザクションは 1 つ（`Db` もマスクされるので入れ子にできない）。`tx` を関数に渡すのは借用だけで、消費は所有する `transaction` ブロックの中でしか起こらない
- `Outbox` はストア操作の一種（`outbox`）として記録するだけで、commit 後に実際に送る処理はモデル化していない
- `Clock`, `Random`, 認可、並行性は扱わない
- 情報フローのラベルは 1 つの秘密レベルにまとめた（ラベルごとの格子はない。異なるラベルを `zip` できないことは、ラベルが 1 つなので現れない）。ラベルつきの値は `Ty.lab` だけで、構造体のフィールドなどに埋め込まれたものはない（コアに集成体がない）。`lbind` の本体は外側の値（ラベルつきのものも含む）を参照できるが、capability は参照できない。表面言語の閉包が可変な状態や関数値を捕捉できないことは、コアでは閉包がない（本体は式）ことで表している
- capability の種類による権限（たとえば `fetch` は `Net` 種別の capability でしか起こらない）は、型安全性（`stuck` にならない）と型付け規則から従う。トレースに対する明示的な定理として述べているのは「渡された capability だけを使う」（定理 2）まで
- 篩型の述語の言語は一般の `*` を含む（意味論上は問題ないが、ソルバが決めるのは線形の場合だけ。非線形の検証条件はソルバが `Unknown` を返し、コンパイラはそれを拒否する）。`/`・`%` の結果についての事実（`opFact`）は出さない
- 整数のオーバーフローは「数学的な整数 + 範囲外で `fault`」としてモデル化した。入力の値そのものが 64bit に収まることは仮定していない（`Val.int` は任意の整数）。`%` はオーバーフローを検査しない（64bit の入力では起こらない）
- 定理 5 の意味論版では、`fetch` の応答が `Net` capability の ID に依存しない oracle を仮定する

## IR の参照インタプリタ `kekkai-ref`

`kek ir -json` が出力する IR（`compiler/ir.kek`）を実行する参照インタプリタです。WasmGC バックエンド（`compiler/wasm_codegen.kek`）とランタイムの prelude（`lib/prelude`、組み込み操作の実装）と同じ意味論を実装し、差分テスト（`tests/run.sh difftest`）の基準になります。

| ファイル | 内容 |
| --- | --- |
| `Kekkai/IR/Arith.lean` | 64 ビット 2 の補数算術（`wrap`、全域な `div`/`rem`、ビット演算）と、その性質の証明（`wrap_inRange`, `wrap_wrap`, `wrap_add_wrap`, `i64Div_min_neg_one` など） |
| `Kekkai/IR/Syntax.lean`, `Value.lean` | IR の構文、実行時の値とヒープ（struct・`Vec` は参照） |
| `Kekkai/IR/Json.lean` | `kek ir -json` の出力（省略されたゼロ値のフィールド、空のリストの `null`）のデコード |
| `Kekkai/IR/Host.lean` | 純粋な host 操作（`int.*`, `bool.to_string`, `string.*`, `log.*`, `clock.now_ms`）。UTF-16 の添字、`split`、WHATWG の UTF-8 デコーダ |
| `Kekkai/IR/Interp.lean` | 明示的なコールスタックを持つスモールステップ機械（fuel について構造的再帰なので全域）と、バックエンドが実装するコレクション操作 `vec.*`, `map.*` |
| `Kekkai/IR/Cli.lean`, `KekkaiRef.lean` | コマンドライン |

ビルドとスモークテスト:

```sh
cd lean && lake build kekkai-ref      # .lake/build/bin/kekkai-ref
lean/test_ref.sh                      # lean/test/smoke.kek などで期待値と比較
tests/run.sh difftest                 # ランダムなプログラムで WasmGC と比較（バイナリがあれば）
```

使い方: `kekkai-ref <ir.json> <関数名> <引数>...`。引数は JSON 値（`42`, `-7`, `true`, `"abc"`, unit は `null`, 集成体は `{"fields":[...]}` / `{"tag":k,"fields":[...]}`、`{"vec":[...]}`、`{"map":[[k,v],...]}`）。型が `ext`（capability）の引数には不透明な値が自動で渡され、コマンドライン引数を消費しません。出力は標準出力に 1 行の JSON で、終了コードは 0:

```sh
$ kekkai-ref basic.json add 9223372036854775807 1
{"ok":-9223372036854775808,"log":[]}
$ kekkai-ref basic.json parse_id '"abc"'
{"ok":{"tag":1,"fields":[{"tag":1,"fields":["abc"]}]},"log":[]}
$ kekkai-ref basic.json greet '{"fields":[1,"bob"]}'
{"ok":null,"log":[["info","hello bob"]]}
$ kekkai-ref basic.json sum_to 1000000
{"error":"timeout"}
```

値の表現: 整数は JSON の数（i64 を正確に）、unit は `null`、variant は `{"tag":k,"fields":[...]}`（その tag のフィールドだけ）、struct は `{"fields":[...]}`、`Vec` は `{"vec":[...]}`（いずれも参照をたどって表示し、循環は `{"cycle":true}`）、host の不透明値は `{"ext":"Log"}`、初期化されていない参照型ローカルは `{"null":true}`。エラーは `timeout`（fuel 1,000,000 ステップ。命令と終端命令が 1 ステップずつ）、`unreachable`、`null dereference`、`trap`（範囲外の `vec.at`）、`stack overflow`（呼び出しの深さ 10000）、`unsupported host op <名前>`、`unsupported await <名前>`（非同期操作は v1 の対象外）。使い方や IR の誤りは標準エラーに出力し、終了コード 1（使い方の誤りは 2）。

意味論の要点（WasmGC と一致させている点）:

- 算術は毎回 `[-2^63, 2^63)` に wrap。`x / 0 = 0`、`x / -1 = 0 - x`（したがって `MIN / -1 = MIN`）、それ以外は 0 方向への切り捨て。`x % 0 = x`、それ以外は被除数の符号を持つ剰余（`Int.tmod`）
- ローカルは型の既定値（`0`, `false`, `ref.null`）で初期化される。`br` は真なら第 1 ターゲット、`switch` は整数の値 `k` と `0, 1, ...` を比べ、どれでもなければ最後のターゲット
- `vfield` で値と異なる tag のスロットを読むと、そのスロットの型の既定値になる（wasm は全 tag のスロットを持ち、使わないスロットを既定値で埋めるため）
- struct・`Vec` はヒープ上の可変オブジェクトで、別名を通した変更が互いに見える（wasm の GC struct/array と同じ）。variant は不変な値。`HashMap` は core ライブラリの普通のコードとして IR に含まれる
- `vec.get`/`vec.set` は負の添字や範囲外で `None`/`false`
- `string.char_at`/`slice`/`index_of` は UTF-16 のコード単位で数え、`slice` は添字を `[0, len]` に切り詰める。`split("")` はコードポイントごとに分ける。`from_char` は `c mod 2^16`、`from_bytes` は各要素 `mod 256` を WHATWG の規則でデコードする（不正な列は U+FFFD、先頭の BOM は除く）。孤立サロゲート（wasm の文字列は持てるが Lean の文字列は持てない）は U+FFFD になる。この場合だけ wasm と異なりうる
- host の `Option` の結果は `None = 0 | Some = 1` の variant に持ち上げる。`string.len` は UTF-16 のコード単位で数え、`string.trim` は Unicode の空白と改行を除く。`to_upper`/`to_lower` は ASCII のみ。`clock.now_ms` は固定値 `1700000000000`
