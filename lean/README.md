# Kekkai コア計算の Lean 形式化

Kekkai の P0 性質（capability 渡しによる副作用の制御と、線形なトランザクション）を、小さなコア計算として Lean 4 で形式化し、証明したものです。設計ドキュメント（`docs/design.md`）の「検証戦略」とロードマップ 1〜2 段階に対応します。

- Lean 4.34.1、依存なし（Mathlib なし、core のみ）
- `sorry` / 独自 `axiom` / `native_decide` なし。主要定理が依存する公理は Lean 標準の `propext`, `Quot.sound`, `Classical.choice` だけ
- ビルド: `cd lean && lake build`（またはリポジトリ直下で `mise run lean`）

## ファイル構成

| ファイル | 内容 |
| --- | --- |
| `Kekkai/Syntax.lean` | 型・capability 種別・値・式・関数定義・プログラム |
| `Kekkai/Basic.lean` | リスト補題（`Forall2`, `lookupAll`） |
| `Kekkai/Typing.lean` | 型付け規則 `HasType`（線形 `Tx` の状態を受け渡す）、`WTProg` |
| `Kekkai/Semantics.lean` | fuel つき big-step 参照インタプリタ `eval`（トレースを出力） |
| `Kekkai/Safety.lean` | 定理 1: 型安全性 |
| `Kekkai/Effects.lean` | 定理 2: エフェクト健全性、純粋関数の系 |
| `Kekkai/Monitor.lean` | トランザクション規律を検査するトレースモニタとその性質 |
| `Kekkai/Linearity.lean` | 定理 3・4: `Tx` の線形性、トランザクション内で取り消せない副作用を禁止 |
| `Kekkai/NoLeak.lean` | 定理 5: capability の非漏洩 |
| `Kekkai/Examples.lean` | 例: 型付けの導出、インタプリタの実行結果（`rfl`）、型エラーになるプログラム |

## コア計算

### 構文

- 値の型 `Ty`: `unit | bool | int`。**capability 型は値の型に含めない**（第二級）
- capability 種別 `CapKind`: `log`, `net`（取り消せない副作用）, `db`（抽象的なトランザクショナルストア）, `tx`（線形）
- 値の変数と capability の変数は、別々の de Bruijn 文脈 `Γ`, `Δ` に置く
- 式 `Expr`:
  - `val`, `var`, `let_`, `ite`, `bin`（整数の `+ - < =`）
  - `call f args caps`: トップレベル関数の呼び出し。引数は A 正規形（値変数と capability 変数の並び）
  - `log c e`, `fetch c e`
  - `transaction d body`: `d : Db` からトランザクションを開始する。`body` の中では capability 変数 0 が新しい `tx` になる
  - `store op c args`: 開いているトランザクションへの、消費しないストア操作。`op ∈ {get, put, delete, outbox}`
  - `commit c`: `Bool` を返す（`true` は成功、`false` は競合などによる失敗）。**どちらの場合もトランザクションは終了する**
  - `rollback c`
  - `abort`: 早期脱出（`?` で `Err` を受けたときに相当）。生きているトランザクションの中では自動で rollback する
- プログラムはトップレベルの一階関数の列。`FunDef` は値引数・capability 引数・返り値の型・本体からなる。capability 引数を持たない関数は純粋

`Db` は SQL に限らない抽象的なトランザクショナルストアです（実体は Durable Objects storage、D1、分散 KV、インメモリなどで、ランタイムアダプタが選ぶ）。ストア操作の応答（`get` の値）と commit の成否は `Oracle` が与え、どの定理も任意の oracle について成り立ちます。

### 型付け `HasType P Γ Δ s e τ s'`

- `Δ : List (Option CapKind)`。`none` は「マスクされた」スロットで、de Bruijn 番号を保ったまま使えなくする
- `s, s' : TxSt = none | live | done` は、最も内側の `transaction` ブロックが所有する `tx` の状態で、評価順に受け渡される（`Δ ⊢ e ⊣ Δ'` 型の線形文脈の受け渡し）
  - `commit` / `rollback` は `live → done`。したがって各経路で高々 1 回
  - `transaction` の本体は `live → done` で型付けする。したがって正常終了する経路では少なくとも 1 回
  - `store` と、関数への `tx` の受け渡し（借用）は `done` でないことを要求する。関数本体は `none` で検査するので、呼び出された側は `tx` を消費できない
  - `abort` は `live → done` を許す（自動 rollback）
- `transaction` の本体は `some tx :: mask Δ` で型付けする。`mask` は `Log` 以外（`Net`, `Db`, 外側の `Tx`）を隠すので、トランザクション内では `Net` を直接にも、関数呼び出し経由でも使えない。トランザクションの入れ子も起こらない
- 関数が well-typed であるとは `HasType P fd.params (fd.caps.map some) .none fd.body fd.ret .none` が成り立つこと

### 意味論 `eval O P fuel env ρ σ e : Result`

- `Result = timeout | stuck | done (o : Outcome) (σ : St) (tr : List Event)`。燃料切れ（`timeout`）と動的型エラー（`stuck`）を区別する
- 実行時の capability は識別子にすぎない。外から渡された資源は `RCap.res id`、`transaction` が作るハンドルは `RCap.tx t`（`t` はカウンタから取る新しい ID）
- イベント `Event`: `log c v | fetch c v | txBegin db t | txOp t op args | txCommit t | txRollback t`
  - 失敗した commit、明示的な rollback、`abort` による自動 rollback は、どれも `txRollback t` になる
- インタプリタは防御的に作ってある。閉じたトランザクションへの操作や、`tx` を消費せずに本体が正常終了することは `stuck` になる
- `runFun O P n f vs caps` はエントリ関数を `caps.map .res` と初期状態で実行する

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

## 定理一覧

| # | 性質 | Lean の名前 | 内容 |
| --- | --- | --- | --- |
| 1 | 型安全性 | `Kekkai.type_safety`（コア補題 `Kekkai.eval_safe`） | well-typed なプログラムのエントリ関数（`Tx` 引数を取らない）を、型の合う引数と capability で実行すると、どんな fuel でも `stuck` にならない。値を返したときは、その値が宣言された返り値の型を持つ |
| 2 | エフェクト健全性 | `Kekkai.effect_soundness`（コア補題 `Kekkai.eval_effects`） | トレース中のすべてのイベントについて、使った資源 capability は渡された `caps` の中にあり、トランザクション ID はその実行中に作られたものである。型付けを仮定せず、意味論だけから成り立つ |
| 2' | 純粋性 | `Kekkai.pure_call_no_effects`, `Kekkai.eval_no_caps` | capability 引数を持たない関数を well-typed に呼び出すと、トレースは空になる |
| 3 | `Tx` の線形性 | `Kekkai.tx_linearity`（`Kekkai.run_txsafe`, `Kekkai.TxSafe.linear`） | トレース中の各 `txBegin _ t` の後には、`t` を終了するイベント（`txCommit t` / `txRollback t`）がちょうど 1 つ現れる。その後 `t` は二度と現れない（ストア操作も、2 回目の commit も起こらない） |
| 4 | トランザクション内で取り消せない副作用を禁止 | `Kekkai.no_irrevocable_in_tx`（`Kekkai.TxSafe.no_irrevocable_inside`） | `txBegin _ t` から `t` の終了までの間に `fetch` は現れない |
| 5 | capability の非漏洩 | `Kekkai.result_independent_of_caps`（コア補題 `Kekkai.eval_rename`）、`Kekkai.no_expr_has_cap_type`, `Kekkai.no_fun_returns_cap`, `Kekkai.Val.capIds_nil` | 型の上では、どの式にもどの関数の返り値にも capability 型は付かない。値の上では、`Val` が capability を含まない（構成上自明）。意味論の上では、渡す capability の ID を任意に付け替えても、結果（返り値・最終状態）は変わらず、トレースの ID だけが付け替わる。つまり結果は capability に依存すらしない |

定理 3・4 は、トレースモニタ `TxSafe`（`Kekkai/Monitor.lean`）を経由して証明しています。モニタは「開いているトランザクションは高々 1 つ」「`txBegin` の ID は未使用のもの」「`txOp`, `txCommit`, `txRollback` は開いているトランザクションに対してだけ」「`fetch` はトランザクションが開いていないときだけ」「最後には何も開いていない」を検査します。`run_txsafe` は、well-typed なプログラムのあらゆる実行（値で終わっても `abort` で終わっても）のトレースがモニタに受理されることを示します。

## 実際の言語からの簡略化

- 値は `Unit`, `Bool`, `Int` だけ。`Result` 型や和型、レコード、篩型はない。`abort` はエラー値を持たず、関数境界を越えて最上位まで伝播する（捕捉する構文はない）
- 関数は一階で、呼び出しは A 正規形
- 同時に開けるトランザクションは 1 つ（`Db` もマスクされるので入れ子にできない）。`tx` を関数に渡すのは借用だけで、消費は所有する `transaction` ブロックの中でしか起こらない
- `Outbox` はストア操作の一種（`outbox`）として記録するだけで、commit 後に実際に送る処理はモデル化していない
- `Clock`, `Random`, 認可、PII、並行性は扱わない
- capability の種類による権限（たとえば `fetch` は `Net` 種別の capability でしか起こらない）は、型安全性（`stuck` にならない）と型付け規則から従う。トレースに対する明示的な定理として述べているのは「渡された capability だけを使う」（定理 2）まで
- 定理 5 の意味論版では、`fetch` の応答が `Net` capability の ID に依存しない oracle を仮定する
