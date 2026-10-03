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
| `Kekkai/IR/*.lean`, `KekkaiRef.lean` | Kekkai IR の実行可能な参照インタプリタ `kekkai-ref`（下記） |

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

## IR の参照インタプリタ `kekkai-ref`

`kek ir -json` が出力する IR（`compiler/ir.kek`）を実行する参照インタプリタです。WasmGC バックエンド（`compiler/wasm_codegen.kek`）とランタイムの prelude（`lib/prelude`、組み込み操作の実装）と同じ意味論を実装し、差分テスト（`tests/run.sh difftest`）の基準になります。

| ファイル | 内容 |
| --- | --- |
| `Kekkai/IR/Arith.lean` | 64 ビット 2 の補数算術（`wrap`、全域な `div`/`rem`、ビット演算）と、その性質の証明（`wrap_inRange`, `wrap_wrap`, `wrap_add_wrap`, `i64Div_min_neg_one` など） |
| `Kekkai/IR/Syntax.lean`, `Value.lean` | IR の構文、実行時の値とヒープ（struct・`Vec`・`Map` は参照） |
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

値の表現: 整数は JSON の数（i64 を正確に）、unit は `null`、variant は `{"tag":k,"fields":[...]}`（その tag のフィールドだけ）、struct は `{"fields":[...]}`、`Vec` は `{"vec":[...]}`、`Map` は挿入順に `{"map":[[k,v],...]}`（いずれも参照をたどって表示し、循環は `{"cycle":true}`）、host の不透明値は `{"ext":"Log"}`、初期化されていない参照型ローカルは `{"null":true}`。エラーは `timeout`（fuel 1,000,000 ステップ。命令と終端命令が 1 ステップずつ）、`unreachable`、`null dereference`、`trap`（範囲外の `vec.at`）、`stack overflow`（呼び出しの深さ 10000）、`unsupported host op <名前>`、`unsupported await <名前>`（非同期操作は v1 の対象外）。使い方や IR の誤りは標準エラーに出力し、終了コード 1（使い方の誤りは 2）。

意味論の要点（WasmGC と一致させている点）:

- 算術は毎回 `[-2^63, 2^63)` に wrap。`x / 0 = 0`、`x / -1 = 0 - x`（したがって `MIN / -1 = MIN`）、それ以外は 0 方向への切り捨て。`x % 0 = x`、それ以外は被除数の符号を持つ剰余（`Int.tmod`）
- ローカルは型の既定値（`0`, `false`, `ref.null`）で初期化される。`br` は真なら第 1 ターゲット、`switch` は整数の値 `k` と `0, 1, ...` を比べ、どれでもなければ最後のターゲット
- `vfield` で値と異なる tag のスロットを読むと、そのスロットの型の既定値になる（wasm は全 tag のスロットを持ち、使わないスロットを既定値で埋めるため）
- struct・`Vec`・`Map` はヒープ上の可変オブジェクトで、別名を通した変更が互いに見える（wasm の GC struct/array と同じ）。variant は不変な値
- `vec.get`/`vec.set` は負の添字や範囲外で `None`/`false`。`Map` は挿入順で、既存のキーへの `insert` は位置を保ち、`remove` の後の `insert` は末尾に付く。キーは値で比較（Int/String/Bool）
- `string.char_at`/`slice`/`index_of` は UTF-16 のコード単位で数え、`slice` は添字を `[0, len]` に切り詰める。`split("")` はコードポイントごとに分ける。`from_char` は `c mod 2^16`、`from_bytes` は各要素 `mod 256` を WHATWG の規則でデコードする（不正な列は U+FFFD、先頭の BOM は除く）。孤立サロゲート（wasm の文字列は持てるが Lean の文字列は持てない）は U+FFFD になる。この場合だけ wasm と異なりうる
- host の `Option` の結果は `None = 0 | Some = 1` の variant に持ち上げる。`string.len` は UTF-16 のコード単位で数え、`string.trim` は Unicode の空白と改行を除く。`to_upper`/`to_lower` は ASCII のみ。`clock.now_ms` は固定値 `1700000000000`
