# todo-app：フロントエンド付きの Todo アプリ

アカウント付きの Todo アプリです。API は Kekkai（`server/`）、画面は素の HTML と JavaScript（`public/index.html`）で、workerd でローカルに動きます。

```sh
mise run todo-app          # http://127.0.0.1:8787 を開く（データは out/todo-app/.state に残る）
mise run todo-app-check    # kek test と kek assure check
```

`mise run todo-app` は `ASSETS=examples/todo-app/public scripts/dev.sh examples/todo-app/server` と同じです。`ASSETS` を付けると、Workers Static Assets と同じく GET で存在するファイル（`/` は `index.html`）は静的ファイルを返し、それ以外を Kekkai のプログラムに渡します。

## 構成

| ファイル | 内容 |
| --- | --- |
| `server/main.kek` | `#[handler]` とルーティング（API の一覧はファイル先頭） |
| `server/auth.kek` | ユーザー登録・ログイン・セッション（HttpOnly・SameSite=Strict の Cookie、7 日） |
| `server/todos.kek` | Todo の保存と、所有者だけが変更できる認可 |
| `server/http.kek` | JSON の本文とエラー（`Json`）、Cookie |
| `public/index.html` | 画面（ビルドなし） |
| `kekkai.toml`・`kekkai.assure.lock` | 保証のポリシーと台帳 |

## 型で守っていること

- **本文は JSON で、型に読み込む**：`#[derive(Deserialize)]` の `Credentials`・`NewTodo` に読み、ストアのレコード（ユーザー、セッション、Todo）も `#[derive(Serialize, Deserialize)]` の struct を JSON で保存します（[docs/serde.md](../../docs/serde.md)）。
- **長さの制約は型**：`type Username = String where 3 <= self.len() && self.len() <= 32` と `type Title = ...`。読み込み時に検査され、違反は `{"error": "username: the value is not a valid `Username`"}` の 400 になります。
- **パスワードは `Labeled<Secret, String>` のまま読まれる**：素の `String` として存在する瞬間がありません。ログ・レスポンス・ストアに渡す、比較する、`if` で分岐するのはどれも型エラーで、`Labeled` は `Serialize` を持たないので JSON にも書けません。外に出せるのは `#[declassify]` を付けた `password_hash`（ソルト付きのハッシュ）と `password_ok`（長さが足りるかの真偽。長さを調べることも格下げなので、型の述語にはできません）だけで、`kek caps` と `kek assure` に記録されます。
- **Todo の変更には `Can<Edit, t>`**：`toggle`・`remove` はその Todo への権限を引数に取り、権限を発行できるのは `#[policy]` の `can_edit`（所有者かどうか）だけです。URL の id で読み込んだ Todo を、ポリシーに聞かずに変更するコードは書けません。他人の Todo は 404 を返します。
- **capability**：`server/todos.kek` は `&Random` を使えず（ポリシーの `forbid`）、アプリ全体で `&Net`・`&Fs` を使えません。

ハッシュは core の `DefaultHasher` を 4096 回重ねたもので、本物のパスワードハッシュ（Argon2 など）ではありません。

## commit で `kek assure plan` を試す

```sh
mise run hooks    # .git/hooks/pre-commit に scripts/assure-pre-commit.sh を足す（既存のフックは残る）
```

フックは、commit が触るプロジェクト（`kekkai.toml` と `kekkai.assure.lock` のあるディレクトリ）で `kek assure check` を実行し、保証が台帳とずれていれば `kek assure plan` を表示して commit を止めます。たとえば：

- `can_edit` に `|| u.name == "admin"` を足す → `change body` が要レビュー（`server/todos.kek` は `review = ["change"]`）
- ログに `password.mask()` を足す → 新しい格下げ（`flow.declassify`）が要レビューで、`#[declassify(...)]` がないのでポリシー違反
- `server/todos.kek` の関数に `random: &Random` を足す → `forbid` の違反

plan を確認して承認するなら、台帳を更新してから commit し直します。

```sh
cd examples/todo-app
../../kek assure plan server
../../kek assure apply -yes server        # 弱化には -reason -owner -expires も要る
git add kekkai.assure.lock
```

フックは作業ツリーを検査します（ステージした内容ではありません）。`git commit --no-verify` で飛ばせます。
