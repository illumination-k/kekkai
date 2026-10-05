# 時刻（`Timestamp`・`Duration`・`Date`・`DateTime`）

時刻の計算は core ライブラリ（`lib/core/time.kek`）の純粋な Kekkai で、副作用は時計を読むことだけ。時計を読むには従来どおり capability が要る。

```kek
fn start_session(clock: &Clock, ...) {
    let expires = Timestamp::now(clock).add(Duration::days(7));
    expires.to_rfc3339()                      // "2026-10-12T09:30:00Z"
}

Timestamp::parse("2026-10-05T18:30:00+09:00")?   // Result<Timestamp, SerdeError>
Date::parse("2024-02-29")?.weekday()              // Weekday::Thu
Duration::parse("1h30m")?                         // 5400000 ms
```

## 決定事項

| 項目 | 決定 |
| --- | --- |
| 時計 | ホストの操作は `clock.now_ms()` のまま増やさない。`Timestamp::now(clock: &Clock)` は core の関数で、`&Clock` を受け取るので副作用（`kek caps` の `Clock`）として現れる。`kek test` のモックの時計（既定 2026-01-01T00:00:00Z、`-clock ms`）がそのまま効く |
| 表現 | `Timestamp` は UNIX 時刻のミリ秒（UTC）、`Duration` はミリ秒。フィールドは `__` で始まる名前なので、作るのは関連関数だけ（`Date` は存在する日付しか作れない） |
| 暦 | 先発グレゴリオ暦（Howard Hinnant の `days_from_civil`）。閏秒は扱わない。文字列としての年は 0000〜9999 |
| タイムゾーン | UTC と固定のオフセット（`DateTime.offset_minutes`）だけ。tz データベースと夏時間の規則は持たない（純粋な計算で、Lean の参照インタプリタと同じ結果になる範囲に留める） |
| 演算 | 演算子のオーバーロードがないのでメソッド（`t.add(d)`・`t.since(&u)`・`d.mul(3)`）。比較は `Ord` を derive しているので `<` などが使える |
| シリアライズ | `Timestamp` は RFC 3339 の UTC（`"2026-10-05T09:30:00.123Z"`。ミリ秒が 0 なら省く）、`Date` は `"YYYY-MM-DD"`、`Duration` は単位付きの文字列（`"1d12h"`）で書く。`Duration` はミリ秒の整数も読める。TOML では日時が文字列として読まれ（serde.md）、日時の形の文字列は引用符なしの TOML の日時として書かれるので、`since = 2026-10-05T18:30:00+09:00` を `Timestamp` に読め、書くときも TOML の日時になる |

## API

| 型 | 主な関数 |
| --- | --- |
| `Timestamp` | `now(clock)`、`from_unix_ms`・`from_unix_seconds`、`unix_ms`・`unix_seconds`、`add`・`sub`（`Duration`）、`since(&earlier) -> Duration`、`is_before`・`is_after`、`date()`（UTC）、`at_offset(minutes) -> DateTime`、`to_rfc3339`、`parse` |
| `Duration` | `ms`・`seconds`・`minutes`・`hours`・`days`、`as_ms`・`as_seconds`（負の無限大へ丸める）、`add`・`sub`・`mul`、`is_negative`、`to_string`（`"1d2h3m4s5ms"`、`"0s"`）、`parse`（`d`・`h`・`m`・`s`・`ms` の並び。符号可） |
| `Date` | `new(y, m, d) -> Option<Date>`、`parse`、`year`・`month`・`day`、`is_leap_year`、`weekday() -> Weekday`（`number()` は月曜 1〜日曜 7）、`add_days`・`days_until`、`days_since_epoch`・`from_days_since_epoch`、`start_of_day() -> Timestamp`（UTC）、`to_string` |
| `DateTime` | フィールド `date`・`hour`・`minute`・`second`・`millis`・`offset_minutes`、`timestamp()`、`to_rfc3339`、`parse`（`T`・`t`・空白の区切り、`Z`・`z`・`±HH:MM`。ミリ秒より細かい端数は切り捨て） |

エラーは `SerdeError`（`no such date`、`no such time of day`、`expected a date-time such as ...`）で、入力の文字列は含めない。

## 未対応

- タイムゾーン名（`Asia/Tokyo`）と夏時間。Workers なら `Intl` を capability として渡す形が考えられる
- オフセットのない日時（TOML の local date-time）の型。今は `Value::Str` のまま
- 月単位の加算（月末の扱いを決める必要がある）
