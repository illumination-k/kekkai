# bootstrap

`kek.wasm` は Kekkai で書かれたコンパイラ（`compiler/`）を、それ自身でコンパイルした WasmGC + WASI のモジュール。
`./kek` はこれを wasmtime で動かして現在の `compiler/` をビルドし（`.kek-cache/` にキャッシュ）、そのコンパイラでコマンドを実行する。

- `./kek bootstrap-check`：現在のコンパイラで自分自身をもう一度ビルドし、不動点（stage1 == stage2）を確認する（CI）
- `./kek bootstrap-update`：不動点を確認したうえで、`kek.wasm` を現在のコンパイラで置き換える

コンパイラに新しい言語機能を使わせるときは、先にその機能を実装して `bootstrap-update` してから使う。prelude（`lib/prelude`）に新しい intrinsic を使わせるときも同じ。
