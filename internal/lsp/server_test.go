package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// client drives a Server over in-memory pipes.
type client struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	id   int
	done chan error
	// notifications received while waiting for responses
	notes []map[string]any
}

func start(t *testing.T) *client {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &client{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan error, 1)}
	go func() {
		err := Serve(inR, outW)
		outW.Close()
		c.done <- err
	}()
	return c
}

func (c *client) send(msg map[string]any) {
	msg["jsonrpc"] = "2.0"
	data, _ := json.Marshal(msg)
	if _, err := fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) read() map[string]any {
	c.t.Helper()
	type res struct {
		m   map[string]any
		err error
	}
	ch := make(chan res, 1)
	go func() {
		body, err := readMessage(c.out)
		if err != nil {
			ch <- res{nil, err}
			return
		}
		var m map[string]any
		err = json.Unmarshal(body, &m)
		ch <- res{m, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			c.t.Fatal(r.err)
		}
		return r.m
	case <-time.After(5 * time.Second):
		c.t.Fatal("timeout waiting for server")
	}
	return nil
}

func (c *client) request(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	c.send(map[string]any{"id": c.id, "method": method, "params": params})
	for {
		m := c.read()
		if id, ok := m["id"].(float64); ok && int(id) == c.id {
			return m
		}
		c.notes = append(c.notes, m)
	}
}

func (c *client) notify(method string, params any) {
	c.send(map[string]any{"method": method, "params": params})
}

// diagnostics waits for the next publishDiagnostics notification.
func (c *client) diagnostics() []any {
	c.t.Helper()
	for {
		var m map[string]any
		if len(c.notes) > 0 {
			m, c.notes = c.notes[0], c.notes[1:]
		} else {
			m = c.read()
		}
		if m["method"] == "textDocument/publishDiagnostics" {
			return m["params"].(map[string]any)["diagnostics"].([]any)
		}
	}
}

const uri = "file:///tmp/test.kek"

const src = `fn helper(s: String) -> Int {
    s.len()
}

fn main2(log: &Log) -> Int {
    let 名前 = 0;
    let x = "日本😀"; let y = helper(x);
    log.info(x);
    y
}
`

func pos(line, char int) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}}
}

func TestServer(t *testing.T) {
	c := start(t)
	init := c.request("initialize", map[string]any{"processId": nil, "rootUri": nil, "capabilities": map[string]any{}})
	caps := init["result"].(map[string]any)["capabilities"].(map[string]any)
	if caps["hoverProvider"] != true || caps["definitionProvider"] != true {
		t.Fatalf("capabilities = %v", caps)
	}
	c.notify("initialized", map[string]any{})

	// Non-ASCII identifiers are a lexer error, reported once per
	// character with UTF-16 ranges (the parser then stops at the `=`).
	c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": uri, "languageId": "kek", "version": 1, "text": src}})
	diags := c.diagnostics()
	if len(diags) != 3 || !strings.Contains(fmt.Sprint(diags[0]), "unexpected character '名'") {
		t.Fatalf("diagnostics = %v", diags)
	}
	r1 := diags[1].(map[string]any)["range"].(map[string]any)
	if r1["start"].(map[string]any)["character"].(float64) != 9 || r1["end"].(map[string]any)["character"].(float64) != 10 {
		t.Errorf("second character range = %v", r1)
	}

	// Fix it (full sync): no diagnostics.
	fixed := strings.Replace(src, "名前", "n", 1)
	c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 2},
		"contentChanges": []any{map[string]any{"text": fixed}}})
	if diags := c.diagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics after fix = %v", diags)
	}

	// Line 6 (0-based): `    let x = "日本😀"; let y = helper(x);`
	// In UTF-16, `helper` starts at 4+8+1+4+1+1+... let us compute it
	// from the text instead of hard-coding.
	line6 := strings.Split(fixed, "\n")[6]
	byteIdx := strings.Index(line6, "helper")
	char := len([]rune(line6[:byteIdx])) + 1 // 😀 is two UTF-16 units, one rune
	hov := c.request("textDocument/hover", pos(6, char+2))
	res, _ := hov["result"].(map[string]any)
	if res == nil {
		t.Fatalf("hover = %v", hov)
	}
	val := res["contents"].(map[string]any)["value"].(string)
	if !strings.Contains(val, "fn helper(s: String) -> Int") || !strings.Contains(val, "**pure**") {
		t.Errorf("hover = %q", val)
	}
	rng := res["range"].(map[string]any)["start"].(map[string]any)
	if int(rng["character"].(float64)) != char {
		t.Errorf("hover range start = %v, want character %d", rng, char)
	}

	// Definition of `x` in `helper(x)` -> `let x` on the same line.
	xUse := strings.Index(line6, "(x)") + 1
	xChar := len([]rune(line6[:xUse])) + 1
	def := c.request("textDocument/definition", pos(6, xChar))
	loc, _ := def["result"].(map[string]any)
	if loc == nil || loc["uri"] != uri {
		t.Fatalf("definition = %v", def)
	}
	st := loc["range"].(map[string]any)["start"].(map[string]any)
	if int(st["line"].(float64)) != 6 || int(st["character"].(float64)) != 8 {
		t.Errorf("definition start = %v, want 6:8", st)
	}

	// Definition of `helper` -> line 0, char 3.
	def = c.request("textDocument/definition", pos(6, char))
	st = def["result"].(map[string]any)["range"].(map[string]any)["start"].(map[string]any)
	if int(st["line"].(float64)) != 0 || int(st["character"].(float64)) != 3 {
		t.Errorf("helper definition = %v", st)
	}

	// Hover on a capability method.
	hov = c.request("textDocument/hover", pos(7, 9))
	val = hov["result"].(map[string]any)["contents"].(map[string]any)["value"].(string)
	if !strings.Contains(val, "Log.info") || !strings.Contains(val, "effect") {
		t.Errorf("method hover = %q", val)
	}

	// Nothing under the cursor: null result.
	if hov := c.request("textDocument/hover", pos(3, 0)); hov["result"] != nil {
		t.Errorf("empty hover = %v", hov)
	}

	syms := c.request("textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}})
	if list := syms["result"].([]any); len(list) != 2 {
		t.Errorf("symbols = %v", syms)
	}

	// Unknown request -> MethodNotFound.
	if r := c.request("textDocument/fooBar", map[string]any{}); r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method = %v", r)
	}

	// A type error is reported with the right range.
	bad := "fn f() -> Int {\n    \"no\"\n}\n"
	c.notify("textDocument/didChange", map[string]any{"textDocument": map[string]any{"uri": uri, "version": 3},
		"contentChanges": []any{map[string]any{"text": bad}}})
	diags = c.diagnostics()
	if len(diags) != 1 {
		t.Fatalf("type diagnostics = %v", diags)
	}
	d := diags[0].(map[string]any)
	r := d["range"].(map[string]any)
	if r["start"].(map[string]any)["line"].(float64) != 1 || r["start"].(map[string]any)["character"].(float64) != 4 ||
		r["end"].(map[string]any)["character"].(float64) != 8 || d["severity"].(float64) != 1 {
		t.Errorf("type diagnostic = %v", d)
	}

	if r := c.request("shutdown", nil); r["error"] != nil {
		t.Fatalf("shutdown = %v", r)
	}
	c.notify("exit", nil)
	select {
	case err := <-c.done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
}
