# Self-hosting 計画

stage0 = Go 実装の `kek`（`cmd/kek`, `internal/*`）。stage1 = Kekkai で書いたコンパイラ（`compiler/`）。

1. stage0 で `compiler/` を WASM にする（`kek run compiler <subcommand> ...` で実行できる）
2. stage1 の出力が stage0 と **バイト単位で一致** することをテストコーパス（`testdata/**/*.kek`）で確認する
3. stage1 で `compiler/` 自身をコンパイルして stage2、stage2 で stage3 を作り、stage2 == stage3 を確認する
4. 一致したら WASM のコンパイラをブートストラップ用に置き、Go 実装を凍結する

## 規約

- `compiler/` は 1 つのプログラム（ディレクトリ内の全 `.kek` が 1 つの名前空間）。モジュールがないので
  **名前にコンポーネントの接頭辞を付ける**：
  - フロントエンド: 型 `Tok*`, `Ast*`、関数 `lex_*`, `parse_*`, `ast_*`
  - 型検査: `Ty*`, `chk_*` / lowering: `lower_*` / IR データ: `Ir*`, `ir_*`
  - バックエンド: `Wasm*`, `wasm_*`, `cg_*` / JSON: `Json*`, `json_*`
  - 共通ユーティリティ: `util_*`（`compiler/util.kek`）
- AST/型/IR のノードは識別のために `id: Int` を持つ（Map のキーは Int か String のみ、ポインタ同一性がないため）。
- エントリは `compiler/main.kek` の `#[main]`。サブコマンドを各コンポーネントの `*_main` 関数へ振り分ける。
- stage0 の出力順序（型・import・関数・ローカル・文字列リテラル表）を忠実に再現する。Go の実装と同じ
  アルゴリズムで書くこと（バイト一致が検証手段）。

## サブコマンド（stage1）

| コマンド | 担当 | 比較対象（stage0） |
| --- | --- | --- |
| `lex <file>` | フロントエンド | `kek tokens <file>` |
| `ast <file>` | フロントエンド | `kek ast <file>`（要追加） |
| `ir2wasm <ir.json> <out.wasm>` | バックエンド | `kek ir -json` → stage0 の wasm.Compile 出力 |
| `check <file>` | 型検査 | `kek check` の診断 |
| `ir <file>` | lowering | `kek ir <file>` のテキスト |
| `build <file> <outdir>` | 全体 | `kek build` |
