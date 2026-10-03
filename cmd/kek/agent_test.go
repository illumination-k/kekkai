package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckJSON(t *testing.T) {
	var buf bytes.Buffer
	ok, err := checkJSON(&buf, "../../testdata/check/err_tx.kek")
	if err != nil || ok {
		t.Fatalf("checkJSON = %v, %v", ok, err)
	}
	var res checkResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.Diagnostics) == 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == "warning" && d.Phase == "lint" {
			continue
		}
		if d.Line == 0 || d.Col == 0 || d.Message == "" || d.Phase != "type" || d.Severity != "error" {
			t.Errorf("bad diagnostic %+v", d)
		}
	}

	buf.Reset()
	ok, err = checkJSON(&buf, "../../testdata/e2e/bank.kek")
	if err != nil || !ok {
		t.Fatalf("bank.kek: %v %v\n%s", ok, err, buf.String())
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"diagnostics": []`)) {
		t.Errorf("want empty diagnostics array, got %s", buf.String())
	}
}

func TestCheckJSONWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.kek")
	os.WriteFile(path, []byte("fn f(log: &Log) -> Int { 1 }\n"), 0o644)
	var buf bytes.Buffer
	ok, err := checkJSON(&buf, path)
	if err != nil || !ok {
		t.Fatalf("warnings must not fail the check: %v %v", ok, err)
	}
	var res checkResult
	json.Unmarshal(buf.Bytes(), &res)
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Severity != "warning" || res.Diagnostics[0].Phase != "lint" {
		t.Fatalf("diagnostics = %+v", res.Diagnostics)
	}
}

func TestCapsJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := capsJSON(&buf, "../../testdata/e2e/bank.kek"); err != nil {
		t.Fatal(err)
	}
	var res capsResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for i, f := range res.Functions {
		byName[f.Name] = i
	}
	tr := res.Functions[byName["transfer"]]
	if tr.Pure || !tr.Async || len(tr.Caps) != 2 || tr.Caps[0].Type != "&Db" || len(tr.UnusedCaps) != 0 {
		t.Errorf("transfer = %+v", tr)
	}
	if want := []string{"db.transaction", "log.info", "tx.commit", "tx.outbox", "tx.put", "tx.rollback"}; len(tr.DirectEffects) != len(want) {
		t.Errorf("transfer effects = %v, want %v", tr.DirectEffects, want)
	}
	pa := res.Functions[byName["parse_amount"]]
	if !pa.Pure || pa.Async || len(pa.Caps) != 0 {
		t.Errorf("parse_amount = %+v", pa)
	}
	if h := res.Functions[byName["handle"]]; !h.Handler {
		t.Errorf("handle = %+v", h)
	}
}

func TestSearchCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := search(&buf, []string{"-json", "String -> Option<Int>", "../../testdata/e2e/bank.kek"}); err != nil {
		t.Fatal(err)
	}
	var res searchResult
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) < 2 || res.Matches[0].Name != "parse_amount" || res.Matches[0].Line != 13 ||
		res.Matches[1].Name != "String.parse_int" || !res.Matches[1].Builtin {
		t.Fatalf("matches = %+v", res.Matches)
	}

	buf.Reset()
	if err := search(&buf, []string{"&Tx, String -> Result<Option<Int>, TxError>", "../../testdata/e2e/bank.kek"}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("read_balance")) {
		t.Errorf("text output = %s", buf.String())
	}
	if err := search(&buf, []string{"String ->"}); err == nil {
		t.Error("bad query accepted")
	}
}
