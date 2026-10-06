# シリアライズ（`Serialize`・`Deserialize`、JSON・TOML）

型と形式のあいだに、共通のデータモデル `Value` を1つ置く。型は `Value` との変換（`Serialize`・`Deserialize`）だけを、形式は `Value` と文字列の変換（parse・render）だけを持つ。実装はすべて core ライブラリ（`lib/core/serde.kek`・`json.kek`・`toml.kek`）で、capability を使わない純粋な Kekkai なので、Lean の参照インタプリタとの差分テストにもそのまま乗る。

```kek
type Port = Int where 0 < self && self < 65536;

#[derive(Serialize, Deserialize)]
struct Server {
    name: String,
    port: Port,
    backup: Option<Port>,
}

let s: Server = Json::from_str(body)?;        // Result<T, SerdeError>
let text = Toml::to_string(&s)?;              // Result<String, SerdeError>
Response::json(200, Json::to_string(&s))
```

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 方式 | serde のように形式ごとの `Serializer` を trait で抽象化するのではなく、中間の `Value` を経由する。derive が生成するのは型ごとに `to_value`／`from_value` の1組で、形式を足しても生成コードも単相化も増えない（型 N 個・形式 M 個で N＋M）。代償は中間の値を確保するコストで、API のペイロードや設定ファイルの大きさでは問題にならない |
| 安全性の規則 | 形式によらず trait の実装で1度だけ決める。`Labeled<L, T>` は `Deserialize` だけを実装し `Serialize` は実装しない（入力は読んだ時点でラベル付き、ラベル付きの値は書き出せない）。`Can<A, r>` と capability はどちらも実装しない（権限を入力から偽造できない） |
| 篩型 | derive した `Deserialize` は、フィールドの型（と `Option`・`Vec` の要素）にある篩型の別名の述語を実行時に調べ、満たさなければ `SerdeError` を返す。調べた条件が篩型の検査器の事実になるので、値の構築に証明が要らない。`Labeled<L, Port>` の中身は調べない（調べた結果がラベル付きの値の性質を漏らすため） |
| エラー | `SerdeError { path, line, col, msg }`。メッセージはデータの中身を含まず、形（`expected an integer, found a string`）と場所（`servers[1].port`、`line 3, column 5`）だけなので、ログやレスポンスに出してよい |
| 名前空間 | モジュールがないので、形式は空の struct の関連関数（`Json::parse`）。`Value`・`Serialize`・`Deserialize`・`SerdeError`・`Json`・`Toml` は core の予約名になる |
| 数値 | 小数・指数のない数は `Value::Int`（64bit、範囲外は parse のエラー）、ある数は `Value::Float`（正しく丸めた `Float`、範囲外は parse のエラー）。`Int` への `Deserialize` は `Float` を拒み、`Float` への `Deserialize` は `Int` も受け取る（JSON の `1` は `Value::Int` なので）。書き出しは読み戻すと同じ値になる最短の桁で、必ず小数部か指数を持つ（`1.0`、`0.1`、`1e21`。`Debug` と同じ）ので、読み戻しても `Float` のまま |
| 可変性 | `Deserialize::from_value(v: &Value)` の結果は `v` から借用したものではなく所有される。core の関数の結果は「共有の引数を受け取ると共有」が既定なので、`clone` と同じ扱いを `#[owned]`（core と prelude だけが書ける）で宣言する。利用者の `from_value` の実装は通常どおり検査される（`&Value` から借用した `Vec` をそのまま返すとエラー） |

## データモデル

```kek
enum Value { Null, Bool(Bool), Int(Int), Float(Float), Str(String), Seq(Vec<Value>), Map(Vec<(String, Value)>) }
```

- `Map` はキーの順序を保つ（JSON・TOML を読んだ順、struct はフィールドの順）。`==` は順序を無視する（キーの重複は parse で拒む）。`Float` は `==` で比べるが、`Value` の `==` では NaN どうしは等しい（`Value` は `Eq` を実装するので同値関係にする。`nan` を含む TOML も往復して等しい）。
- `v.get(key) -> Option<&Value>`、`v.kind()`（`"an integer"` など）。

| 型 | `Value` |
| --- | --- |
| `Int`・`Float`・`Bool`・`String` | `Int`・`Float`・`Bool`・`Str` |
| `()` | `Null` |
| `Option<T>` | `None` は `Null`、`Some(x)` は `x` |
| `Vec<T>` | `Seq` |
| `HashMap<String, V>` | `Map` |
| `Value` | そのまま |
| `Labeled<L, T>` | `Deserialize` のみ（`T` として読んでラベルを付ける） |
| `Timestamp`・`Date`・`Duration` | `Str`（RFC 3339、`YYYY-MM-DD`、`"1h30m"`。[time.md](time.md)） |
| derive した struct | フィールドの `Map`（フィールドの順）。ないフィールドは `Null` として読む（`Option` なら `None`、それ以外は `missing field`） |
| derive したタプル構造体・ユニット構造体 | serde と同じ：`struct S;` は `Null`、`struct S(T);`（newtype）は中身の値そのもの、`struct S(A, B);` は `Seq` |
| derive した enum | 外部タグ：フィールドなしは `"V"`、1つは `{"V": x}`、2つ以上は `{"V": [x, y]}`、構造体のようなバリアント `V { a: A, b: B }` は `{"V": {"a": x, "b": y}}`（ないフィールドは struct と同じく `Null` として読む） |

`#[derive(Serialize, Deserialize)]` は generic な型にも使える（`impl<T: Serialize> Serialize for Wrap<T>`）。`Labeled` を含む型には `Serialize` を derive できない（エラー）。

## JSON（`Json`）

| 関数 | 内容 |
| --- | --- |
| `Json::parse(s) -> Result<Value, SerdeError>` | RFC 8259。重複したキー、先頭の 0、制御文字、末尾のデータはエラー。入れ子は 256 段まで |
| `Json::render(&v)`・`render_pretty(&v)` | 1行／2スペースの字下げ。JSON にない NaN・無限大は `null`（JavaScript と同じ） |
| `Json::from_str::<T>(s)`・`to_string(&x)`・`to_string_pretty(&x)` | 型との変換 |

## TOML（`Toml`）

| 関数 | 内容 |
| --- | --- |
| `Toml::parse(s) -> Result<Value, SerdeError>` | TOML 1.0：テーブル、テーブルの配列、ドット付きのキー、インラインテーブル、4種の文字列、16・8・2 進数と `_`、浮動小数点数（`inf`・`nan` も。書き出しは最短の桁）。日時（4種）は書かれたとおりの `Str`（存在する日付・時刻であること）。テーブルの再定義、重複したキー、閉じたインラインテーブルの拡張はエラー |
| `Toml::render(&v) -> Result<String, SerdeError>` | 文書は `Map` であること。各テーブルは普通のキーを先に、子のテーブルとテーブルの配列（要素がすべて `Map` の空でない `Seq`）を後に書く。子のテーブルしかないテーブルのヘッダは省く。日時の形の文字列（10 文字以上）は引用符なしの TOML の日時として書く（読むと `Str` に戻るので値は変わらない）。`Null` のキーは書かない（TOML に null はない）。配列の中の `Null` はエラー |
| `Toml::from_str::<T>(s)`・`to_string(&x)` | 型との変換 |

コンパイラの `kekkai.toml` もこれで読む（`compiler/tool_common.kek` の `tool_toml_parse`）。

## 未対応

- フィールドの属性（`#[serde(rename = "...")]`、`default`、`skip`）。構文がフィールドの属性を持たないため
- YAML
