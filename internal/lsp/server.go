// Package lsp implements a minimal Language Server Protocol server for
// Kekkai over JSON-RPC 2.0 (stdio framing with Content-Length headers),
// using only the standard library. It supports full document sync,
// diagnostics (parse, type and unused-capability lint), hover,
// go-to-definition and document symbols.
//
// Positions: the compiler uses 1-based line/column over bytes; LSP uses
// 0-based lines and UTF-16 code units. The conversion lives in
// tooling.Doc.
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
	"sync"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/tooling"
)

// Server is a Kekkai language server.
type Server struct {
	out  io.Writer
	wmu  sync.Mutex
	docs map[string]*tooling.Analysis

	shutdown bool
}

// Serve runs the server until `exit` or EOF. It returns nil on a clean
// exit (after `shutdown`).
func Serve(in io.Reader, out io.Writer) error {
	s := &Server{out: out, docs: map[string]*tooling.Analysis{}}
	r := bufio.NewReader(in)
	for {
		body, err := readMessage(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		var msg request
		if err := json.Unmarshal(body, &msg); err != nil {
			s.reply(nil, nil, &rpcError{Code: -32700, Message: "parse error: " + err.Error()})
			continue
		}
		if msg.Method == "exit" {
			if s.shutdown {
				return nil
			}
			return fmt.Errorf("exit without shutdown")
		}
		s.handle(&msg)
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	tp := textproto.NewReader(r)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		if err == io.EOF || (len(hdr) == 0 && strings.Contains(err.Error(), "EOF")) {
			return nil, io.EOF
		}
		return nil, err
	}
	n, err := strconv.Atoi(hdr.Get("Content-Length"))
	if err != nil || n < 0 {
		return nil, fmt.Errorf("lsp: bad Content-Length header")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (s *Server) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(data))
	s.out.Write(data)
}

func (s *Server) reply(id json.RawMessage, result any, rerr *rpcError) {
	msg := map[string]any{"jsonrpc": "2.0", "id": id}
	if id == nil {
		msg["id"] = nil
	}
	if rerr != nil {
		msg["error"] = rerr
	} else {
		msg["result"] = result
	}
	s.write(msg)
}

func (s *Server) notify(method string, params any) {
	s.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// ---- protocol types ----

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type textDocumentPosition struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position position `json:"position"`
}

func rangeOf(d *tooling.Doc, start, end syntax.Pos) lspRange {
	sl, sc := d.ToLSP(start)
	el, ec := d.ToLSP(end)
	return lspRange{position{sl, sc}, position{el, ec}}
}

// ---- dispatch ----

func (s *Server) handle(m *request) {
	isRequest := len(m.ID) > 0
	result, rerr := s.dispatch(m)
	if isRequest {
		s.reply(m.ID, result, rerr)
	}
}

func (s *Server) dispatch(m *request) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		return map[string]any{
			"capabilities": map[string]any{
				"positionEncoding":       "utf-16",
				"textDocumentSync":       map[string]any{"openClose": true, "change": 1},
				"hoverProvider":          true,
				"definitionProvider":     true,
				"documentSymbolProvider": true,
			},
			"serverInfo": map[string]any{"name": "kek-lsp", "version": "0.1"},
		}, nil
	case "initialized", "$/cancelRequest", "$/setTrace", "workspace/didChangeConfiguration", "textDocument/didSave":
		return nil, nil
	case "shutdown":
		s.shutdown = true
		return nil, nil
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, invalidParams(err)
		}
		s.update(p.TextDocument.URI, p.TextDocument.Text)
		return nil, nil
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Range *lspRange `json:"range"`
				Text  string    `json:"text"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, invalidParams(err)
		}
		if n := len(p.ContentChanges); n > 0 {
			// Full sync: the last change carries the whole text.
			s.update(p.TextDocument.URI, p.ContentChanges[n-1].Text)
		}
		return nil, nil
	case "textDocument/didClose":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, invalidParams(err)
		}
		delete(s.docs, p.TextDocument.URI)
		s.notify("textDocument/publishDiagnostics", map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{}})
		return nil, nil
	case "textDocument/hover":
		a, pos, err := s.at(m.Params)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, nil
		}
		text, start, end, ok := a.Hover(pos)
		if !ok {
			return nil, nil
		}
		return map[string]any{
			"contents": map[string]any{"kind": "markdown", "value": text},
			"range":    rangeOf(a.Doc, start, end),
		}, nil
	case "textDocument/definition":
		var p textDocumentPosition
		json.Unmarshal(m.Params, &p)
		a, pos, err := s.at(m.Params)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, nil
		}
		start, end, ok := a.Definition(pos)
		if !ok {
			return nil, nil
		}
		return map[string]any{"uri": p.TextDocument.URI, "range": rangeOf(a.Doc, start, end)}, nil
	case "textDocument/documentSymbol":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, invalidParams(err)
		}
		a := s.docs[p.TextDocument.URI]
		if a == nil {
			return []any{}, nil
		}
		return symbols(a.Doc, a.Symbols()), nil
	}
	if len(m.ID) > 0 {
		return nil, &rpcError{Code: -32601, Message: "method not found: " + m.Method}
	}
	return nil, nil // unknown notifications are ignored
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
}

func (s *Server) at(params json.RawMessage) (*tooling.Analysis, syntax.Pos, *rpcError) {
	var p textDocumentPosition
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, syntax.Pos{}, invalidParams(err)
	}
	a := s.docs[p.TextDocument.URI]
	if a == nil {
		return nil, syntax.Pos{}, nil
	}
	return a, a.Doc.FromLSP(p.Position.Line, p.Position.Character), nil
}

func (s *Server) update(uri, text string) {
	a := tooling.Analyze(text)
	s.docs[uri] = a
	diags := []any{}
	for _, d := range a.Diags {
		sev := 1
		if d.Severity == "warning" {
			sev = 2
		}
		diags = append(diags, map[string]any{
			"range":    rangeOf(a.Doc, d.Pos, d.End),
			"severity": sev,
			"source":   "kek",
			"code":     d.Phase,
			"message":  d.Message,
		})
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": diags})
}

var symbolKinds = map[string]int{"function": 12, "struct": 23, "enum": 10, "field": 8, "variant": 22}

func symbols(d *tooling.Doc, syms []tooling.Symbol) []any {
	out := []any{}
	for _, sy := range syms {
		m := map[string]any{
			"name":           sy.Name,
			"kind":           symbolKinds[sy.Kind],
			"range":          rangeOf(d, sy.Start, sy.End),
			"selectionRange": rangeOf(d, sy.SelStart, sy.SelEnd),
		}
		if sy.Detail != "" {
			m["detail"] = sy.Detail
		}
		if len(sy.Children) > 0 {
			m["children"] = symbols(d, sy.Children)
		}
		out = append(out, m)
	}
	return out
}
