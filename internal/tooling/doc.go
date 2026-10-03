// Package tooling implements the editor and agent services of the kek
// toolchain on top of the parser and type checker: position mapping,
// diagnostics, hover, go-to-definition, document symbols, completion,
// capability reports and type-directed search. The LSP server
// (internal/lsp) and the JSON outputs of `kek check`, `kek caps` and
// `kek search` are thin layers over this package.
package tooling

import (
	"unicode/utf16"
	"unicode/utf8"

	"github.com/illumination-k/kekkai/internal/syntax"
)

// Doc is a source text with a line index. It converts between syntax.Pos
// (1-based line, 1-based byte column) and LSP positions (0-based line,
// 0-based UTF-16 code unit offset).
type Doc struct {
	Src   string
	lines []int // byte offset of the start of each line
}

// NewDoc indexes src.
func NewDoc(src string) *Doc {
	d := &Doc{Src: src, lines: []int{0}}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			d.lines = append(d.lines, i+1)
		}
	}
	return d
}

// LineCount returns the number of lines.
func (d *Doc) LineCount() int { return len(d.lines) }

// Line returns the text of 0-based line n without its line terminator.
func (d *Doc) Line(n int) string {
	if n < 0 || n >= len(d.lines) {
		return ""
	}
	start := d.lines[n]
	end := len(d.Src)
	if n+1 < len(d.lines) {
		end = d.lines[n+1] - 1 // drop '\n'
	}
	if end > start && d.Src[end-1] == '\r' {
		end--
	}
	return d.Src[start:end]
}

// Offset returns the byte offset of p, clamped to the text.
func (d *Doc) Offset(p syntax.Pos) int {
	line := p.Line - 1
	if line < 0 {
		return 0
	}
	if line >= len(d.lines) {
		return len(d.Src)
	}
	off := d.lines[line] + p.Col - 1
	if off < d.lines[line] {
		off = d.lines[line]
	}
	if off > len(d.Src) {
		off = len(d.Src)
	}
	return off
}

// PosAt converts a byte offset to a syntax.Pos.
func (d *Doc) PosAt(off int) syntax.Pos {
	if off < 0 {
		off = 0
	}
	if off > len(d.Src) {
		off = len(d.Src)
	}
	lo, hi := 0, len(d.lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if d.lines[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return syntax.Pos{Line: lo + 1, Col: off - d.lines[lo] + 1}
}

// ToLSP converts a syntax.Pos to a 0-based line and UTF-16 character.
func (d *Doc) ToLSP(p syntax.Pos) (line, char int) {
	line = p.Line - 1
	if line < 0 {
		return 0, 0
	}
	if line >= len(d.lines) {
		line = len(d.lines) - 1
		return line, utf16Len(d.Line(line))
	}
	text := d.Line(line)
	n := p.Col - 1
	if n < 0 {
		n = 0
	}
	if n > len(text) {
		n = len(text)
	}
	return line, utf16Len(text[:n])
}

// FromLSP converts a 0-based line and UTF-16 character to a syntax.Pos.
// Positions past the end of a line are clamped to the line end.
func (d *Doc) FromLSP(line, char int) syntax.Pos {
	if line < 0 {
		return syntax.Pos{Line: 1, Col: 1}
	}
	if line >= len(d.lines) {
		last := len(d.lines) - 1
		return syntax.Pos{Line: last + 1, Col: len(d.Line(last)) + 1}
	}
	text := d.Line(line)
	units, i := 0, 0
	for i < len(text) && units < char {
		r, size := utf8.DecodeRuneInString(text[i:])
		if n := utf16.RuneLen(r); n > 0 {
			units += n
		} else {
			units++
		}
		i += size
	}
	return syntax.Pos{Line: line + 1, Col: i + 1}
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if k := utf16.RuneLen(r); k > 0 {
			n += k
		} else {
			n++
		}
	}
	return n
}

// Less orders positions.
func Less(a, b syntax.Pos) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}
