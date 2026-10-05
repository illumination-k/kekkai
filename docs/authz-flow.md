# P2：認可と情報フロー

設計の背景は [design.md](design.md)（保証する性質の P2、「認可と情報フロー」）。ここでは決定事項と実装を書く。言語としての説明は [language.md](language.md#認可cana-r)。

**実装済み**：認可（`Can<A, r>`、`#[policy]`）、情報フロー型（`Labeled<L, T>`。P1 の `Pii<T>` を統合）、`kek caps`・`kek assure`・`kek fix` との統合、Lean の非干渉性の定理。テナント分離は見送った（下記）。

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 資源の表し方 | 変数で索引付ける。`Can<Edit, d>` の `d` は引数か不変な変数の名前で、型の一部になる（軽量な singleton 型。依存型は入れない） |
| 権限の発行 | `#[policy]` を付けた関数だけが `Can::grant()` を呼べる。ポリシー自体の正しさは対象外（設計のノンゴール）で、`kek assure` の前提として記録する |
| 暗黙のフロー | `Labeled<L, T>` のモナドで防ぐ。中身の計算は `map`・`zip`・`and_then` に渡すクロージャの中だけで、結果は同じラベルで包まれる。分岐はその中でしか起きないので、プログラムカウンタのラベルはクロージャのラベルそのもの |
| クロージャの制限 | 中身を読み取り専用（`shared`）で受け取り、書き換えられる状態（`shared` 以外の、可変な状態に届く値）と関数の値を捕捉できない。capability は元々捕捉できない |
| `Pii<T>` | 廃止して `Labeled<PII, T>` に統合した。`kek fix` が `Pii<T>` → `Labeled<PII, T>`、`Pii::new(x)` → `PII::label(x)` に書き換える |
| ラベル | 型。core に `PII` を置き、利用者は任意の struct（`struct Secret {}`）をラベルにできる。ラベルの束はなく、違うラベルの値は `zip` で混ぜられない（入れ子 `Labeled<A, Labeled<B, T>>` は可） |
| テナント分離 | 今回は見送り。資源と同じ変数の索引で `Labeled<Tenant<t>, T>` として表せる見込み |

## 認可（`Can<A, r>`）

```kek
struct Edit {}

#[policy(reason = "owners edit their documents", owner = "alice")]
fn can_edit(u: User, d: Doc) -> Option<Can<Edit, d>> {
    if d.owner == u.id { Some(Can::grant()) } else { None }
}

fn rename(mut d: Doc, t: String, _cap: Can<Edit, d>) { ... }

impl Doc {
    fn retitle(self, t: String, cap: Can<Edit, self>) { rename(self, t, cap) }
}

match can_edit(user, doc) {
    Some(cap) => rename(doc, "new", cap),   // OK
    None => {}
}
rename(other, "new", cap)                  // 型エラー：the permission is for `doc`, not `other`
```

実装（`compiler/chk_authz.kek`、型は `compiler/chk_types.kek` の「resource paths」、core は `lib/core/can.kek`）での決定：

| 項目 | 決定 |
| --- | --- |
| 型の中の資源 | `Ty::Opaque("@" + 識別子 + ":" + 名前)`。引数の識別子は `f:<関数名>#<番号>`、局所変数は束縛の ID。同じ識別子どうしだけが一致するので、同名の別の変数（シャドーイング）も区別する。表示は名前だけ（`Can<Edit, d>`） |
| 書ける場所 | 関数のシグネチャ（その関数の引数。`self` も可）と、本体の型注釈（スコープにある変数）。struct・enum のフィールド、クロージャの型には書けない（名前が解決できずエラー）。資源の位置に型を書くとエラー |
| 資源にできる変数 | 付け替えられない束縛：引数（`mut x: T` も束縛は付け替えられない）と `let`。`let mut` はエラー（付け替えると権限が別の値を指す）。中身の書き換えは追わない（可変性の追跡で別に扱う） |
| 呼び出し | シグネチャが引数を資源として名指す関数（`ChkInfo.path_fns`）の呼び出しでは、その位置の実引数が変数でなければエラー。引数・戻り値の型の資源を実引数の資源で置き換えてから検査する。メソッドの受け手も同じ |
| 関数の値 | 資源を名指す関数は値として使えない（呼び出しでの置き換えができないため） |
| 漏洩 | 局所変数の権限は戻り値の型（引数しか名指せない）と一致しないので、関数の外に出ない。`Option`・`Vec` などに入れて関数内で使うのは自由 |
| 発行 | `Can::grant()` は `#[policy]` の関数（とその中のクロージャ）の中だけ。`Can { ... }` はフィールド `__granted` が予約名なので書けない。`#[policy]` は `reason`・`owner`・`expires` を取れる |
| 実行時 | `Can` はフィールド 1 つの struct。資源は実行時の表現を持たない（`ty_key` で `@` に潰すので、資源ごとに型や関数の実体が増えない） |
| 診断 | `mismatched types in argument of `edit`: expected `Can<Edit, b>`, found `Can<Edit, a>` (the permission is for `a`, not `b`)`。同名の別変数なら `(the permission is for another variable named `a`)` |
| 未対応 | 不変な別名（`let d2 = d;`）を同じ資源とみなすこと、ID の等しさ（`a.id == b.id`）を篩型で示して権限を移すこと、trait のメソッドのシグネチャでの資源 |

## 情報フロー（`Labeled<L, T>`）

```kek
struct Customer {
    id: Int,
    email: Labeled<PII, String>,
}

let domain = c.email.map(|e| e.split("@").get(1).unwrap_or(""));  // Labeled<PII, String>
let same = a.zip(&b, |x, y| x == y);                               // Labeled<PII, Bool>
log.info(domain);            // 型エラー：labeled data cannot be used as a plain value
if a == b { ... }            // 型エラー：labeled values are compared inside `zip`
log.info(domain.mask());     // OK：格下げ（kek caps と kek assure に記録）
```

`lib/core/labeled.kek`：

| API | 意味 |
| --- | --- |
| `PII::label(x)`、`Labeled::new(x)` | 包む（`new` のラベルは期待される型から決まる） |
| `l.map(f: fn(&T) -> U) -> Labeled<L, U>` | 中身の計算 |
| `l.zip(&m, f: fn(&T, &U) -> V) -> Labeled<L, V>` | 同じラベルの 2 つを組み合わせる |
| `l.and_then(f: fn(&T) -> Labeled<L, U>) -> Labeled<L, U>` | 中身からラベル付きの値を作る |
| `l.mask() -> String`（`T = String`）、`l.hash() -> String`（`T: Hash`）、`l.expose_unchecked() -> T` | 格下げ（P1 の `Pii` と同じ） |
| `Clone` | `T: Clone` なら |

実装での決定：

| 項目 | 決定 |
| --- | --- |
| 不透明さ | P1 の `Pii` と同じ。フィールド `__value` は core の外から書けず、表示・比較・順序・ハッシュの trait を実装しない。P1 で残った `==`（候補を包んで比べる総当たり）もなくした：`==` は `zip` の中で書き、結果はラベル付き |
| クロージャの検査 | 可変性のパス（`compiler/chk_mut.kek` の `mu_flow_closure`）で行う。view を知っているため。`map`・`zip`・`and_then` に渡すクロージャの引数は `shared`（中身を書き換えると、包む前の別名から読めてしまう）。捕捉した変数は、型が関数の値を含みうる（`fn` 型、型パラメータ、それらを含む struct・enum）ならエラー、可変な状態に届く型で `shared` でなければエラー（`let v` の `own` な `Vec` も、`let mut m = v` で移して書き換えられるので不可） |
| 関数を渡すとき | クロージャ式か名前付きの関数だけ。関数の値（変数に入ったクロージャ）は捕捉した状態を書き換えうるのでエラー。名前付きの関数は可変な状態に届く引数を `&T` で受け取らなければならない（`items.iter().map(f)` と同じ規則） |
| 診断の phase | クロージャの検査の誤りは `kek check -json` で phase `flow`。ラベルの型の誤りは `type`（ラベル違いには `(values labeled `Secret` and `PII` cannot be combined)` を添える） |
| `#[derive]` | `Labeled` を含む型には `Clone` だけ（等しさも順序もハッシュも、ラベル付きの値を素の値に変える） |
| 非干渉性 | 格下げしないプログラムでは、素の出力（ログ・レスポンス・ストア・戻り値の素の部分）はラベル付きの入力に依存しない。終了と時間のチャネルは対象外（TINI）。証明はコア計算の `lean/Kekkai/Flow.lean`（下記） |
| `kek fix` | 型検査の前に字句で `Pii<` → `Labeled<PII, `、`Pii::new` → `PII::label` に書き換える（利用者のプログラムだけ）。`Pii` どうしの `==` は直せないので、エラーとして残り手で `zip` に直す |

## ツール

- `kek caps`：`#[policy]` の関数に `#[policy]`、権限を受け取る関数に `requires: Edit(d)`、返す関数に `grants: Edit(d)`。JSON は全関数に `policy`・`requires`・`grants`。格下げの一覧（`declassify`）は以前と同じ。
- `kek assure`：
  - 前提 `policy`：`#[policy]` の関数ごとに 1 つ（理由・責任者・期限は属性から）。新しいポリシーはレビュー対象。
  - 保証 `authz.requires.<A>(<r>)`（真偽、根拠 `type`）：権限を受け取る関数。権限を受け取らなくなると弱化。
  - 保証 `flow.noninterference`（真偽、根拠 `type`）：ラベル付きの値を受け取る・返す・格下げする関数（到達する関数を含む）に、到達する関数のどれも格下げしないかを記録する。格下げを始めると弱化。
  - 格下げの前提は `pii.declassify` から `flow.declassify` に、設定の節は `[pii]` から `[flow]` に改名した（`[pii]` も同じ意味で読む。設定のハッシュは変えない）。

## Lean

`lean/Kekkai/Flow.lean`（詳細は [lean/README.md](../lean/README.md)）：コア計算にラベル付きの値の型 `Ty.lab`、`wrap e`（`Labeled::new`）、`lbind e body`（`and_then`。本体は capability なし・トランザクションなしで型付けし、本体の `abort` は局所的な失敗の値になる）を加え、`log`・`fetch`・`store` は公開の型の値だけを受け取る。定理 `noninterference`：well-typed なプログラムの関数を、公開の引数が等しい 2 つの環境で実行し、両方が終わったなら、トレースと最終状態が等しく、結果は低等価（公開の型なら等しい：`noninterference_public`）。ラベルは 1 つの秘密の水準にまとめ、格下げはモデルに含めない。

## テスト

`testdata/run/authz.kek`・`labeled.kek`、`testdata/check/err_authz.kek`・`err_flow.kek`・`err_labeled.kek`・`err_labeled_private.kek`、`tests/suites/assure.sh` の `authz`・`pii`、`tests/agent_cmds`（`caps` の `requires`・`grants`、`check -json` の phase `flow`）。
