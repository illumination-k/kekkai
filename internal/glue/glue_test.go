package glue

import (
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/ir"
)

var params = []ir.HandlerParam{
	{Name: "req", Kind: "request"},
	{Name: "db", Kind: "Db"},
	{Name: "log", Kind: "Log"},
}

func TestWorkerJS(t *testing.T) {
	js := WorkerJS(params)
	for _, want := range []string{
		`import wasm from "./module.wasm";`,
		`"db": dbCap(db("DB"), outbox(env)),`,
		`"log": consoleLog(),`,
		"export class KekkaiObject",
		"async queue(batch, env)",
		"env.KEKKAI_DO",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("worker.js lacks %q", want)
		}
	}
	if strings.Contains(js, `"req"`) {
		t.Error("the request parameter is not a capability")
	}
}

func TestWranglerToml(t *testing.T) {
	d1, err := WranglerTomlFor("bank", params, TargetD1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d1, "[[d1_databases]]\nbinding = \"DB\"") || strings.Contains(d1, "durable_objects") {
		t.Errorf("d1 target:\n%s", d1)
	}
	if WranglerToml("bank", params) != d1 {
		t.Error("WranglerToml must default to the d1 target")
	}
	do, err := WranglerTomlFor("bank", params, TargetDO)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`name = "KEKKAI_DO"`, `class_name = "KekkaiObject"`, `new_sqlite_classes = ["KekkaiObject"]`, `KEKKAI_DO_SHARD = "global"`} {
		if !strings.Contains(do, want) {
			t.Errorf("do target lacks %q:\n%s", want, do)
		}
	}
	if strings.Contains(do, "d1_databases") {
		t.Errorf("do target binds D1:\n%s", do)
	}
	if _, err := WranglerTomlFor("bank", params, "kv"); err == nil {
		t.Error("unknown target accepted")
	}
}
