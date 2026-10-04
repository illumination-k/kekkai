# 保証の台帳と `kek assure plan`

## 目的

証明できることに加え、「何が、何を根拠に、どんな前提で保証されているか」を人間が把握できるようにする。人間はコードを全部読まず、保証の変化だけをレビューする。

## 基本方針

- 注釈と脱出口はコードの側に書き、そこから台帳を自動生成する（二重管理しない）。
- 保証すべき性質は設定ファイルに1か所で宣言する。
- 正本はgitのロックファイル。kek サーバーは計算と表示を担うキャッシュで、gitからいつでも再構築できる。

## 保証のデータモデル（最初に固める）

定義ハッシュごとに次を持つ。

| 項目 | 内容 |
| --- | --- |
| 保証 | 成り立つ性質（pii.no_leak、net.hosts、契約など） |
| 根拠 | 型検査／SMTによる証明／テスト／仮定 |
| 前提 | SMTソルバ、外部境界の宣言、脱出口、declassify、`#[allow(similar)]`、`#[rare]` |
| 脱出口の管理情報 | 理由、責任者、期限 |

## コマンド

| コマンド | 役割 |
| --- | --- |
| `kek assure plan` | ロックファイルと実際の保証の差分を表示する。JSON出力が基本で、人間向け表示はその上に作る |
| `kek assure apply` | 人間の承認のもとでロックファイルを更新する |
| `kek assure check` | CI 用。ロックファイルと実際の保証がずれていれば失敗する |

- 変化は「強化／変更／弱化／新しい前提」に分類する。
- 弱化には理由・責任者・期限を必須にする。期限切れはCIで検出する。
- ロックファイルと実際の保証がずれていればCIが失敗する（ドリフト検出）。

## 表示（リスクの種類単位でまとめる）

```
$ kek assure plan
Needs review (1):
  ! weaken effects: +Log   rate_key
    -> app/rates.kek:19  escalate: owner (apply needs -reason, -owner, -expires)

Auto-approved (3): strengthen 1, allowed host 2
```

- 人間に見せるのはポリシーから外れた変更だけ。自動承認分は必要時のみ展開する（`-v`）。
- 同じ変化（たとえば呼び出し元へ伝わる通信先の追加）は1行にまとめる（`fetch_rate (+1 more: convert)`）。
- SMTの証明には、使われた事実（unsat core）を「12行目の if により〜」のような説明として付ける。失敗時は反例を示す。（未実装）
- 自然言語の要約は補助表示に限る。判定は構造化されたplanとポリシーで行う。

## 設定ファイル（planの項目に対するルール）

```toml
[net]
allowed_hosts = ["eutils.ncbi.nlm.nih.gov", "api.stripe.com"]

[pii]
max_declassify_per_module = 2
declassify_requires = ["reason", "owner", "expires"]

[auto_approve]
strengthen = true
new_contract = true

[escalate]
weaken = "owner"
new_extern_decl = "core"
```

- planと同じ語彙（保証名、種類、モジュール、変化の向き）で書く。
- 設定言語は判定が決定可能な範囲に絞り、ルール同士の矛盾を検出できるようにする。
- 組織 → リポジトリ → モジュールで継承する。下位は厳しくする方向にのみ上書きでき、どの版の設定を使ったかはロックファイルにハッシュで固定する。

## kek サーバーの役割

- 定義単位のキャッシュでplanを即座に計算する。
- LSP経由で編集中にplanを表示する。
- エージェントがコミット前にAPIでplanを取得し、自分で修正できるようにする。
- 複数エージェントの保証の衝突を事前に検出し、衝突しないロックファイルの変更は自動マージする。

## 実装の順番

1. 保証のデータモデル（実装済み）
2. ロックファイルの形式（定義単位）（実装済み）
3. planの差分計算とJSON出力（実装済み）
4. 設定ファイル（許可リストと「弱化は要承認」から始める）（実装済み）
5. 設定の階層と継承（最小版を実装済み：`[assure] extends` と `[module."path"]`）

kek サーバー、LSP、SMT の根拠、`new_extern_decl` は未実装。`[pii]` は最小版の個人情報の型（`Pii<T>`）に対して実装済み（下記）。

---

# 現在の実装

コンパイラ（`compiler/assure_*.kek`、接頭辞 `asr_`）が実装し、`./kek assure ...` で動く。定義ハッシュ（`compiler/defhash.kek`、`kek hash`）の上に作っている。

```sh
kek assure plan  [-json] [-v] [-strict] <file|dir>
kek assure apply [-yes] [-reason r] [-owner o] [-expires YYYY-MM-DD] [-renew] <file|dir>
kek assure check [-json] <file|dir>
# 共通: -lock f（既定は [assure] lock、なければ kekkai.assure.lock）
#       -config f（既定は ./kekkai.toml。なければ空のポリシー）
#       -today YYYY-MM-DD（ランチャーが date +%Y-%m-%d で渡す。KEK_TODAY で上書きできる）
```

パスはすべてカレントディレクトリ（`kekkai.toml` のある場所、リポジトリのルート）からの相対で、ロックの `file` もそこからの相対パス。

## 保証（コンパイラが今日確立できるもの）

関数（`impl` のメソッドを含む）ごとに、名前をキーとして次を記録する。

| 保証 | 種類 | 根拠 | 内容 |
| --- | --- | --- | --- |
| `effects` | 集合（上限） | `type` | 引数で受け取る capability の種類（`Log`, `Net`, `Db`, `Fs`, `Clock`, `Random`, `Tx`）。capability は第二級でアンビエントな権限がないので、これが関数とその呼び出し先が起こしうる副作用の上限になる。空なら純粋 |
| `net.hosts` | 集合（上限） | `type` | `net.get`・`net.post`・`tx.outbox` の URL から読み取った通信先ホスト。呼び出しグラフで到達できる関数の分を含む。URL が文字列リテラルか、ホストの終わり（`/`・`?`・`#`）まで含むリテラルで始まる `+` の連結のときだけホストが分かり、それ以外は `*`（不明） |
| `tx.linear` | 真偽 | `type` | `db.transaction` を開く関数。`Tx` の線形性（commit／rollback をちょうど1回）を型検査器が保証している |
| `refine.index_safe`・`refine.no_div_zero`・`refine.no_overflow` | 真偽 | `smt` | 関数の `v[i]` の範囲・除数（0 でも `MIN / -1` でもない）・`+ - *` のオーバーフローの検証条件が、その種類について 1 つ以上あり、すべて篩型の検査器（`compiler/refine.kek`、ソルバは `compiler/smt.kek`）で証明できた。`kekkai.toml` の `[refine]` にかかわらず全関数で全種類を調べる。検証条件のない種類は省く。成り立たなくなると弱化。推論は数学的な整数で行うので、`refine.no_overflow` のない関数の他の証明はラップアラウンドがないことを前提にする（[refinement.md](refinement.md)） |
| `idempotent` | 真偽 | `type` | capability を受け取る関数で、呼び出しグラフで到達できる capability の操作がすべて冪等（リトライしても状態が変わらない：読み取り、ログ、時計、`tx.put`・`tx.delete`、トランザクション）。偽なら省く。成り立たなくなると弱化（関数が純粋になった場合は除く）。`#[handler(idempotent)]` はこれを型エラーとして要求する |
| `tested` | 真偽 | `test` | いずれかの `#[test]` から呼び出しグラフ（定義ハッシュの依存）で到達できる。`#[test]` 自身には付かない |

保証のほかに次を持つ。

- `hash`：定義ハッシュ（名前に依存しない。変数名の変更では変わらない）
- `file`、`entry`（`handler`・`main`・`test`・空）、`async`（I/O に到達するのでステートマシンになる）
- `assumptions`：コード側の前提。`#[allow(similar, reason = "...", owner = "...", expires = "YYYY-MM-DD")]` と `#[rare]`、個人情報の格下げ（`pii.declassify`：`Pii` の `mask`・`hash`・`expose_unchecked` の呼び出し 1 つにつき 1 つ。ロックには `call` を記録し、理由・責任者・期限はその関数の `#[declassify(reason = "...", owner = "...", expires = "YYYY-MM-DD")]` から取る）。同じ種類の前提は順番で対応づけるので、格下げの追加・削除・`#[declassify]` の変更が plan に現れる（位置は `pii.declassify mask() at 24:46` のようにメッセージに出る）
- `waivers`：`apply` で承認した弱化の記録（理由・責任者・期限・承認日）

集合の保証は「この範囲のことしかしない」という上限なので、要素が増えると弱化、減ると強化。真偽の保証は成り立たなくなると弱化。将来の保証（`pii.*`、契約）や根拠（`smt`）も同じ形（名前・種類・値・根拠）で追加できる。

## ロックファイル `kekkai.assure.lock`

JSON。キーの順序は固定で、定義は名前順、配列の要素は1行に1つなので、保証の変化が小さな git の差分になる。

```json
{
  "version": 1,
  "config": "39e08b1a536bf81fa473387097946df7",
  "definitions": {
    "rate_key": {
      "hash": "dfb979a79a4083fafbdae9ef5e8f218e",
      "file": "app/rates.kek",
      "entry": "",
      "async": false,
      "guarantees": {
        "effects": {"value": ["Log"], "evidence": "type"},
        "net.hosts": {"value": ["api.rates.example"], "evidence": "type"},
        "tx.linear": {"value": true, "evidence": "type"},
        "tested": {"value": false, "evidence": "test"}
      },
      "assumptions": [
        {"kind": "allow(similar)", "reason": "kept apart from price_key", "owner": "shogo", "expires": "2027-03-31"}
      ],
      "waivers": [
        {"guarantee": "effects", "items": ["Log"], "reason": "audit log", "owner": "shogo",
         "expires": "2027-01-31", "approved": "2026-10-04"}
      ]
    }
  }
}
```

（実際のファイルは `kek` の JSON 出力と同じく2スペースで字下げし、配列も1要素1行で書く。）

- `version`：形式の版（現在 1）。知らない版は設定エラー。
- `config`：ロックを承認したときの実効ポリシーのハッシュ。ポリシーの中身（書式ではなく）が変わると plan に `change policy` が出る。
- `guarantees`：値が配列なら集合、真偽なら真偽の保証。`net.hosts` は空なら、`tx.linear` は偽なら省く（ないものは空集合／偽として比べる）。知らない名前の保証もそのまま比べる。
- `waivers`：承認した弱化。`items` はそのとき増えた要素（真偽の保証なら空）。その要素がまだ残っている間だけ保持し、なくなれば次の `apply` で消える。

## plan の変化の分類

| class | rule | いつ |
| --- | --- | --- |
| `strengthen` | `strengthen` | 集合の要素が減った、真偽の保証が成り立つようになった、前提が消えた |
| `strengthen` | `new_contract` | ロックになかった保証が新たに成り立つ（例：`tx.linear`） |
| `change` | `change` | 本体だけが変わり保証は同じ（`change body (guarantees unchanged)`）、`file`・`entry`・`async` の変化、`tx.linear` の消失（トランザクションを開かなくなった）、名前の変更（同じハッシュの定義が消えて現れた：`rename: a -> b`） |
| `weaken` | `weaken` | 集合に要素が増えた、真偽の保証が成り立たなくなった（例：`tested`） |
| `weaken` | `allowed_host` | `net.hosts` に増えたホストがすべてその場所の `allowed_hosts` に含まれる |
| `assumption` | `new_assumption` | 新しい前提、または前提の理由・責任者・期限の変更 |
| `added` / `removed` | `added` / `removed` | 定義の追加・削除 |
| `change` | `config` | ポリシーのハッシュが変わった |

本体だけの変更（ハッシュだけが変わる）は保証が同じなので `change` として既定で自動承認する。コードのレビューはコードレビューの役目で、台帳は保証の変化だけを扱う。モジュールで `review = ["change"]` とすればそのファイルでは要レビューにできる。

ポリシーの判定とは別に、現在のプログラムの状態について次を調べる。

- **ポリシー違反**（承認できない。コードかポリシーを直す）：その場所で禁止された capability（`forbid`）、`allowed_hosts` にないホスト（不明な `*` を含む）、日付として読めない `expires`、`[pii] declassify_requires` の項目が `#[declassify]` にない格下げ（`declassify_requires`）、`[pii] max_declassify_per_module` を超えて格下げするファイル（`max_declassify_per_module`、上限を超えた最初の格下げの位置に出す）
- **期限切れ**：`expires` が今日より前の `#[allow(similar)]` と、ロックの `waivers`

## ポリシー（`kekkai.toml`）

```toml
[assure]
lock = "kekkai.assure.lock"              # ロックファイルの場所
extends = "../org/kekkai-policy.toml"    # 上位（組織）の設定。厳しくする方向にのみ上書きできる
require = ["reason", "owner", "expires"] # エスカレートした変化の承認に必要な項目（既定は3つすべて）

[net]
allowed_hosts = ["api.rates.example", "hooks.warehouse.example"]  # あれば、これ以外への通信はポリシー違反

[effects]
forbid = ["Random"]                      # どこでも禁止する capability

[auto_approve]                           # rule ごとの自動承認（既定値）
strengthen = true
new_contract = true
change = true
allowed_host = true
added = true
removed = true
new_assumption = false
config = false
# weaken は常に要レビュー（true は矛盾としてエラー）

[escalate]                               # 承認に -reason/-owner/-expires が要る rule と、その担当
weaken = "owner"                         # 既定。外せない
new_assumption = "security"              # 任意

[pii]                                    # 個人情報（Pii）の格下げ（mask・hash・expose_unchecked）
max_declassify_per_module = 2            # 1 ファイル（モジュール）あたりの格下げの上限
declassify_requires = ["reason", "owner"] # 格下げする関数の #[declassify(...)] に必須の項目

[module."app/billing"]                   # このファイル・ディレクトリ以下だけに効く（厳しくする方向のみ）
allowed_hosts = ["api.stripe.com"]       # 上位の allowed_hosts の部分集合でなければエラー
forbid = ["Net"]                         # 追加の禁止
review = ["change", "added"]             # 上位で自動承認でも、ここでは要レビュー
```

- モジュールのパスはファイル（`app/rates.kek`）かディレクトリ（`app`）。入れ子のモジュールはすべて適用され、許可ホストは共通部分、禁止と要レビューは和集合になる。
- `extends` は再帰的にたどる（深さ 8 まで）。下位の `allowed_hosts` は上位の部分集合、上位で要レビューの rule を下位で `true` にはできない。`forbid`・`escalate`・`require` は合わせる。
- 知らないキー、知らない capability・rule、上記の「緩める」上書き、`[auto_approve] X = true` と `[escalate] X` の両立、`forbid` に `Net` があるのに `allowed_hosts` が空でないこと、は設定エラーとして報告する（plan・check・apply は終了コード 1）。
- `[pii]` は `extends` で継承し、下位は `max_declassify_per_module` を小さくする方向にのみ変えられる（`declassify_requires` は合わせる）。`[pii]` を書かない設定のハッシュは変わらない。
- ほかのツールのセクション（`[similar]` など）は無視する。

## apply

1. 設定エラーやポリシー違反があれば拒否する。
2. 要レビューの変化があれば `-yes` を要求する（WASI からは対話できないので、承認はフラグで表す）。
3. エスカレートした変化（既定では弱化）には `[assure] require` の項目（既定 `-reason`・`-owner`・`-expires`）を要求する。`-expires` は今日以降の日付。承認は `waivers` に記録する。
4. ロックを書き直す。内容が同じなら書かない。
5. 期限切れの `waivers` は `-renew -reason ... -owner ... -expires ...` で更新できる。コード側の `#[allow(similar)]` の期限はコードを直す。

## 終了コード

| コマンド | 0 | 1 |
| --- | --- | --- |
| `plan` | それ以外 | 設定エラー、ポリシー違反、`-strict` で期限切れ |
| `apply` | ロックを更新した（または最新） | 拒否した |
| `check` | ロックが最新で、違反も期限切れもない | ロックがない・ずれている・設定エラー・ポリシー違反・期限切れ |

使い方の誤りは 2。`check` は期限切れでも失敗する（警告だけにしたい CI は `plan` を使う）。

## CI への組み込み

```yaml
- run: ./kek assure check app      # ロックが古い・要レビュー・違反・期限切れで失敗
```

エージェントやレビューの補助には `kek assure plan -json` を使う。JSON の形式は [tooling.md](tooling.md)。このリポジトリのテストは `tests/suites/assure.sh`（`testdata/assure` のプログラム・ポリシー・ロックを複製して編集し、`tests/assure/golden` と比べる）。

## 制限

- 保証は関数単位。struct・enum の型宣言の変化は、それを使う関数の定義ハッシュの変化としてだけ現れる。
- `net.hosts` は URL のリテラルからしか読めない。変数を経由した URL は `*`（不明）になり、`allowed_hosts` があればポリシー違反になる。
- `tested` は呼び出しグラフでの到達で、実行されたかどうか（カバレッジ）ではない。
- 期限の判定には今日の日付が要る。コンパイラは時計を持たないので、`./kek` ランチャーが `-today` を渡す（直接 wasm を動かすと期限は判定しない）。
