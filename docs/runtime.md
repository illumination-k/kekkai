# Kekkai ランタイムとストアアダプタ

この文書は、コンパイル済みのKekkaiプログラムがCloudflare Workers（とテスト用のNode）でどう動くか、特にトランザクションを実際のストレージへ対応づける**ストアアダプタの契約**を説明する。言語側の設計は [design.md](design.md) を参照。

- ランタイム本体：`js/kekkai_runtime.js`（`kek build` が `kekkai_runtime.js` として出力する）
- Workersのエントリポイント：`internal/glue/glue.go` が `worker.js` と `wrangler.toml` を生成する
- 例：`examples/`（todo、冪等な決済、Webhookのファンアウト）

## 1. 何を言語が決め、何をアダプタが決めるか

Kekkaiの `Db` / `Tx` は**バックエンドに依存しない**。言語が固定するのはトランザクションの**プロトコル**だけで、どのストレージに、どの方式で書くかはランタイムが選ぶアダプタが決める。

```rust
let r = db.transaction(|tx| {
    let a = tx.get("balance:alice")?;      // 失敗したら自動でrollback
    tx.put("balance:alice", "70")?;
    tx.outbox("https://hooks.example/x", "alice paid");  // commit成功後に実行
    tx.commit()?;                          // 楽観的競合なら retryable な TxError
    Ok(())
});
```

### 保証の分担

| 性質 | 誰が保証するか | 仕組み |
| --- | --- | --- |
| `Tx` はちょうど1回 commit か rollback される | 言語（型検査） | 線形な `Tx`。commit漏れ・二重commit・commit後の使用は型エラー |
| tx内でロールバック不能な副作用を起こさない | 言語（型検査） | `Net` と `Db`（ネストしたトランザクション）はtxブロック内でマスクされる |
| 外部への通知はcommit成功後だけ | 言語＋ランタイム | `tx.outbox` に積み、ランタイムの `Transaction.commit` が成功後にだけ配送する。commit失敗・rollback時は破棄 |
| `?` や `Err` で抜けたら自動rollback | 言語＋ランタイム | コンパイラが `tx.finish` を挿入し、開いたままのtxをランタイムがrollbackする |
| commitが失敗したら何も残さない | ランタイム＋アダプタ | 失敗したcommitの後にランタイムが `rollback()` を呼ぶ。アダプタは部分適用をしてはならない |
| 原子性（commitの書き込みは全部か無か） | **アダプタ** | D1のアトミックなbatch、DOの `storage.transaction()` など |
| 分離レベル | **アダプタ** | 同梱アダプタはすべて直列化可能（serializable） |
| 競合の検出と報告 | **アダプタ** | 競合時は `TxConflict`（`retryable() == true`）を投げる |
| commit後の永続性 | **アダプタ** | バックエンドの永続化に従う |
| 競合時に再実行するか | **プログラム** | `e.retryable()` を見て、トランザクション全体を再実行する（`examples/todo` 参照） |

言語の保証は「プログラムがプロトコルを守る」ことまでで、「プロトコルを守ったプログラムが正しいデータを得る」ことはアダプタの義務になる。アダプタが分離レベルを弱めれば（例：スナップショット分離）、型検査を通ったプログラムでもwrite skewは起こりうる。そのため同梱アダプタはすべて同じ適合テスト（§6）を通す。

**注意：`Log` はtx内で使えるが、取り消されはしない。** `Log`・`Clock`・`Random` は「外部の状態を変えない」という意味でrevocable扱いになっており、tx内で呼べる。しかしログの出力そのものはrollbackで消えないので、commitに失敗したトランザクションのログ行も残る（`testdata/e2e/bank.kek` をworkerdで動かすと、競合した転送の `transfer alice -> bob` も出力される）。ログは「試みたこと」の記録として読むこと。

## 2. アダプタの契約

```ts
interface Store {
  begin(): Promise<StoreTx>
  get(key: string): Promise<string | null>   // トランザクション外の読み取り（commit済みの値）
}
interface StoreTx {
  get(key: string): Promise<string | null>
  put(key: string, value: string): Promise<void>
  delete(key: string): Promise<void>
  commit(): Promise<void>     // 競合なら TxConflict を投げる
  rollback(): Promise<void>
}
```

各操作の義務：

- **`get`**：同じtxで書いた値を返す（read-your-writes）。削除済みなら `null`。
- **`put` / `delete`**：commitまで他のトランザクションやトランザクション外の `get` に見えてはならない。
- **`commit`**：書き込みを原子的に適用する。直列化可能性を壊す競合があれば何も適用せず `TxConflict` を投げる。一時的な障害（ビジー、過負荷、到達不能）は `new TxError(msg, { retryable: true })`、それ以外は `retryable: false` の `TxError` を投げる。それ以外の例外もランタイムが `TxError`（retryableでない）に変換する。
- **`rollback`**：何も適用しない。commit失敗の後にも呼ばれるので、何度呼ばれても安全であること。
- **分離レベル**：直列化可能を要求する。最低限、lost update、write skew、存在しないキーへの同時挿入（phantom）、delete→再作成によるABAを検出すること。
- 読み取りだけのトランザクションもcommit時に読み取りを検証する（同梱アダプタの挙動）。異なる時点の値を組み合わせた結果を返さないためである。

アダプタは `dbCap(store, outbox)` でcapabilityになる。`outbox` はcommit後に `{ url, body }` を受け取る関数である。

## 3. 同梱アダプタ

### MemoryStore

テストとローカル開発用。キーごとのバージョンによる楽観的並行性制御（OCC）。

### D1KvStore（Workersのデフォルト）

D1をバージョン付きのキー・バリュー表として使う楽観的トランザクション。

- 読み取りは値とバージョンを記録し、書き込みはメモリに溜める。
- commitは**1回のD1 batch**（D1はbatchをSQLiteの1トランザクションとして実行する）。先頭で読んだキーのバージョンを再検証し、変わっていれば `CHECK (conflict = 0)` 制約に違反する行をguard表へ挿入してbatch全体を失敗させる。成功すれば書き込みを適用する。この制約違反を `TxConflict` に変換する。
- バージョンは**表全体で単調増加する時計**（`<t>_clock` の1行）から採る。キーごとのカウンタだと、`delete` の後に再作成されたキーがバージョン1に戻り、古い読み手が別の値に対して検証を通してしまう（ABAによるlost update）。旧実装にあったこのバグは適合テストの「delete + re-create」で再現でき、修正済み。

スキーマ（初回使用時に作成される。`D1KvStore.schema()` でも取得できる）：

```sql
CREATE TABLE IF NOT EXISTS kekkai_kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS kekkai_kv_guard (conflict INTEGER CHECK (conflict = 0));  -- 常に空
CREATE TABLE IF NOT EXISTS kekkai_kv_clock (id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL);
INSERT OR IGNORE INTO kekkai_kv_clock (id, n) SELECT 0, COALESCE(MAX(ver), 0) FROM kekkai_kv;
```

初期データは `wrangler d1 execute DB --local --file seed.sql` で投入できる。上の4文の3文目と4文目の間に `INSERT INTO kekkai_kv VALUES ('balance:alice', '100', 1), ...` を入れれば、時計は既存の最大バージョンから始まる（`tests/suites/workers.mjs` がこの手順を使う）。

D1は単一ライタなので、スループットはD1データベース1つ分が上限になる。

### DurableObjectStore

Durable Object（DO）の中で、そのトランザクショナルストレージ（`ctx.storage`、KV／SQLiteどちらのバックエンドでもよい）を使う。DOは単一スレッドで強整合なストレージを持つので、Workersで対話的なトランザクションを行う自然な場所である。

- 読み取りは観測した**値**を記録し、書き込みはメモリに溜める。
- commitは `storage.transaction()` の中で読んだキーを読み直し、値が変わっていなければ書き込みを適用する（変わっていれば `TxConflict`）。検証と書き込みが原子的なので、値の比較で十分である：commit時点で読んだ値がすべて現在値と等しいなら、トランザクション全体をcommit時点で実行したのと同じ結果になる。
- DOの中でもプログラムが `await` するたびに他のリクエストが割り込みうるので検証は必要だが、実際にはinput gateのおかげで競合はまれである（workerdでの測定：同じDOへの24並列転送はすべて競合なしでcommitされた。D1では大半が競合した）。
- キーには `prefix`（生成コードではバインディング名＋`:`、例：`DB:balance:alice`）が付く。

### RemoteKvStore（分散ストア向けの骨組み）

HTTPのゲートウェイ越しに楽観的KVを使う汎用アダプタ。ゲートウェイの参照実装（スナップショット読み取り付きのMVCC）が `tests/e2e/fakes.mjs` にある。

```
POST {base}/begin                          -> { "readVersion": any }
POST {base}/get    { key, readVersion }     -> { "value": string|null, "version": any }
POST {base}/commit { readVersion,
                     reads:  [{ key, version }],
                     writes: [{ key, value|null }] }
     -> 200 { "ok": true }   原子的に適用した
     -> 409                  競合（TxConflict）
     -> 503 / 429            一時的な障害（retryable な TxError）
```

ゲートウェイの義務は「`reads` の検証と `writes` の適用を原子的に行う」ことだけである。`readVersion` があれば、MVCCストアは1つのトランザクションのすべての読み取りを同じスナップショットから返せる。バージョンはアダプタにとって不透明な値である。

## 4. 分散ストアへの対応づけ

いずれも「読み取り集合を記録し、commit時に検証して書き込む」という同じ形に落ちる。

| ストア | begin | get | commit | 競合 → `TxConflict` |
| --- | --- | --- | --- | --- |
| FoundationDB | `getReadVersion()` を `readVersion` に | スナップショット読み取りではなく通常の読み取り（キーが読み取り競合範囲に入る） | 書き込みを積んで `commit()`。FDBが読み取り競合範囲を検証する（直列化可能） | `not_committed`（1020）→ 409。`commit_unknown_result`（1021）は冪等でない限りretryableにしない |
| TiKV（楽観的トランザクション） | PDから `start_ts` | `start_ts` のスナップショット読み取り | Percolatorの2相commit（prewrite → commit）。読み取りキーは `lock_keys` などで書き込み競合の検査対象に含める（含めないとスナップショット分離になりwrite skewを許す） | `WriteConflict` → 409 |
| Spanner | read-writeトランザクションを開始 | ロック付き読み取り（悲観的） | `Commit`。外部整合（直列化可能より強い） | `ABORTED` → 409（クライアントライブラリの自動リトライは切り、Kekkai側で再実行する） |
| DynamoDB | なし（`readVersion` は使わない） | `GetItem`（`ConsistentRead: true`）で値と `ver` 属性を得る | `TransactWriteItems`：読んだ各キーに `ConditionCheck`（`ver = :seen`、存在しなかったキーは `attribute_not_exists`）、書き込みは `Put` / `Delete`（`ver` を新しい値に）。1回最大100項目 | `TransactionCanceledException` の `ConditionalCheckFailed` → 409、`TransactionConflict` → 409 |
| PostgreSQL / CockroachDB | `BEGIN ISOLATION LEVEL SERIALIZABLE` | `SELECT`（同じ接続） | `COMMIT` | SQLSTATE `40001` → 409 |

接続を保持する必要があるバックエンド（Spanner、PostgreSQL）では、ゲートウェイが `readVersion` の代わりにトランザクションIDを返し、`/get` と `/commit` で受け取るようにプロトコルを拡張する。トランザクション本体ではプログラムが `Net` を使えないので、トランザクションの寿命はストア操作の往復だけで決まり、長時間ロックを握る心配は小さい。

## 5. 生成される worker.js の設定

`kek build -o out app.kek` は `module.wasm`、`kekkai_meta.js`、`kekkai_runtime.js`、`worker.js`、`wrangler.toml`（既存なら上書きしない）を出力する。capabilityを作るのは `worker.js` だけで、プログラムが権限を受け取る場所はここに限られる。

| 設定 | 意味 |
| --- | --- |
| `[[d1_databases]] binding = "DB"` | `db: &Db` 引数のバックエンド（デフォルト）。引数名を大文字にしたバインディング名を使う |
| `[[durable_objects.bindings]] name = "KEKKAI_DO"`, `class_name = "KekkaiObject"` | これがあると、すべてのリクエストをDOへ転送し、`Db` はDOのストレージを使う |
| `KEKKAI_DO_SHARD` | どのDOへ送るか：`global`（デフォルト、1つ）、`segment:N`（パスのN番目）、`header:NAME`、`query:NAME` |
| `KEKKAI_OUTBOX` | outboxの配送方法：`fetch`（デフォルト）、`log`、`queue` |
| `[[queues.producers]] binding = "OUTBOX"` | `KEKKAI_OUTBOX = "queue"` のときの送信先。同じWorkerの `queue()` ハンドラがコンシューマとして配送する |

`kek build -target=do` はDO用の `wrangler.toml`（`KEKKAI_DO` バインディングとSQLiteバックエンドのマイグレーション）を生成する。`worker.js` 自体はどちらのターゲットでも同じで、`KEKKAI_DO` バインディングの有無で切り替わる。

**シャーディングの注意**：トランザクションが原子的なのは1つのDOの中だけである。`KEKKAI_DO_SHARD` のキーは、1つのトランザクションが触るキーをすべて含むように選ぶ（例：テナントIDをパスの先頭に置き `segment:0`）。異なるDOのデータは別のストアになる。

**outboxの配送保証**：

- `fetch`：commit後に `ctx.waitUntil` でPOSTする。失敗はログに出るだけで再送しない。commitと配送の間でisolateが落ちれば失われる（at-most-once）。
- `queue`：commit後にCloudflare Queuesへ積み、コンシューマが再送付きで配送する（at-least-once）。受け手は冪等であること。
- `log`：配送せずに出力する（ローカル開発・テスト用）。

commitとoutboxの記録を同じトランザクションにする本当の意味での「トランザクショナルoutbox」は今後の課題である（§8）。

### ローカルで動かす

```sh
mise run dev -- examples/todo/todo.kek            # D1（ローカル）
TARGET=do mise run dev -- examples/todo/todo.kek  # Durable Objects
```

`scripts/dev.sh` は `wrangler dev --local` を使い、Cloudflareのアカウントもネットワークも要らない（Miniflare/workerdとローカルのD1・DO）。outboxはデフォルトで `log` になる。

## 6. テスト

| コマンド | 内容 |
| --- | --- |
| `mise run e2e` | Node上のe2e（`testdata/e2e/*.kek`、`examples/*/*.kek`）、アダプタ適合テスト、`*.bad.kek` が期待どおり型エラーになることの確認 |
| `mise run e2e-workers` | `bank.kek` をビルドし、`wrangler d1 execute` でD1に初期データを入れ、`wrangler dev --local` 上でHTTPで動かす（D1とDOの両方） |

- **適合テスト**（`tests/e2e/adapters.test.mjs`）：lost update、write skew、phantom、delete→再作成（ABA）、読み取りだけのトランザクションの検証、blind write、失敗したcommitの原子性、並行インクリメントの再試行を、MemoryStore／D1KvStore／DurableObjectStore／RemoteKvStoreのすべてに対して確認する。新しいアダプタはここに加える。
- **偽のD1**（`tests/e2e/fakes.mjs` の `FakeD1`）：`node:sqlite` の上に `prepare / bind / first / run / all / raw / batch / exec` を実装する。`batch` は本物と同じく1つのSQLiteトランザクションで実行され、制約違反で全体がrollbackされる。各操作の前にイベントループへ制御を返すので、並行するトランザクションは実際に交互に実行される。`before` フックで、commitの直前に別の書き手を割り込ませることもできる。
- **workerd**（`tests/suites/workers.mjs`、`node tests/run.mjs workers`）：WasmGCモジュールがworkerdで読み込めること、`import wasm from "./module.wasm"` が動くこと、D1KvStoreとDurableObjectStoreが本物のローカルバックエンドで動くこと、commit直前に割り込ませた書き込みで競合（503）になり何も適用されないこと、24並列のリクエストで残高が保存されること、outboxがcommitしたトランザクションの分だけ配送されることを確認する。テスト用のエントリ `tests/workers/test_worker.js` は生成された `worker.js` を変更せずに包む。wranglerがない環境と `--short` ではスキップされる。

## 7. 新しいアダプタを書く

1. `Store` / `StoreTx` を実装する（§2）。読み取り集合の記録とcommit時の検証が基本形である。
2. 競合は `TxConflict`、一時的な障害は `retryable: true` の `TxError` にする。
3. `tests/e2e/fakes.mjs` の `allStores` に加え、適合テストと全e2e（例のプログラムもすべてのアダプタで動く）を通す。
4. Workersで使うなら、`worker.js` の `capabilities(env, db)` に渡す `db(binding)` を差し替える。生成コードを編集する代わりに、`examples` と同じく生成物をimportして包むエントリを書くとよい（`tests/workers/test_worker.js` が実例）。

## 8. 今後の課題（言語・コンパイラ側の変更が必要なもの）

- **トランザクショナルoutbox**：outboxをcommitと同じ原子的書き込みでストアに記録し、配送はその記録から行う（クラッシュしても失われない）。ランタイムだけでも `StoreTx.commit(outboxEntries)` の拡張で実装できるが、配送の重複を受け手が判別できるよう、言語側でoutboxエントリに冪等キーを持たせたい。
- **Logの扱い**：tx内のログをcommit時までバッファするか、`Log` を「試行の記録」として型で区別するかを決める（§1の注意）。
- **再試行の標準化**：現在は各プログラムが `while` で再実行を書いている（`examples/todo`）。`db.transaction` に再試行回数を渡す構文か標準ライブラリ関数があるとよい。クロージャは再実行しても安全である（不可逆な効果を持てないため）ことを型が保証しているので、言語がこれを提供するのは筋がよい。
- **JSONとエスケープ**：文字列のエスケープ・置換・分割がないため、例では引用符を含む入力を拒否している。
- **タプル**：多値を返すには構造体が必要（`examples/payments` の `PaymentRequest`）。
- **シャードキーの型付け**：DOルーティングのシャードキーと、トランザクションが触るキーの対応はいまは規約にすぎない。キーの型にシャードを含めれば、「1つのトランザクションは1つのシャードに収まる」ことを型検査できる。
