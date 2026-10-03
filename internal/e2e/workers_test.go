//go:build unix

package e2e

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestWorkers runs testdata/e2e/bank.kek on workerd through
// `wrangler dev --local` (Miniflare, local D1 and Durable Objects; no
// network or Cloudflare account) and drives it over HTTP with
// workers/bank.workers.mjs. It covers loading the WasmGC module in
// workerd, the generated worker.js, D1KvStore and DurableObjectStore on
// the real local backends, a forced optimistic conflict on each, real
// concurrent requests, and the outbox (KEKKAI_OUTBOX=log).
//
// Skipped when wrangler is not installed or with -short. Run alone with
// `mise run e2e-workers`.
func TestWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	wrangler, err := exec.LookPath("wrangler")
	if err != nil {
		t.Skip("wrangler not found (installed by mise: `mise run e2e-workers`)")
	}
	node := nodePath(t)

	dir := t.TempDir()
	compileTo(t, "../../testdata/e2e/bank.kek", dir)
	tw, err := os.ReadFile("workers/test_worker.js")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("test_worker.js", string(tw))
	write("wrangler.toml", `name = "kekkai-e2e"
main = "test_worker.js"
compatibility_date = "2026-09-01"

[vars]
KEKKAI_OUTBOX = "log"
KEKKAI_DO_SHARD = "global"

[[d1_databases]]
binding = "DB"
database_name = "kekkai-e2e"
database_id = "local"

[[durable_objects.bindings]]
name = "TEST_DO"
class_name = "TestKekkaiObject"

[[migrations]]
tag = "v1"
new_sqlite_classes = ["TestKekkaiObject"]
`)
	// Seed D1 the way a user would: the adapter's schema plus rows. The
	// clock row starts at the highest version present.
	write("seed.sql", `CREATE TABLE IF NOT EXISTS kekkai_kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS kekkai_kv_guard (conflict INTEGER CHECK (conflict = 0));
CREATE TABLE IF NOT EXISTS kekkai_kv_clock (id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL);
INSERT INTO kekkai_kv (k, v, ver) VALUES ('balance:alice', '100', 1), ('balance:bob', '5', 1);
INSERT OR IGNORE INTO kekkai_kv_clock (id, n) SELECT 0, COALESCE(MAX(ver), 0) FROM kekkai_kv;
`)
	env := append(os.Environ(), "WRANGLER_SEND_METRICS=false", "CI=1", "NO_COLOR=1")
	state := filepath.Join(dir, "state")
	seed := exec.Command(wrangler, "d1", "execute", "DB", "--local", "--persist-to", state, "--file", "seed.sql", "-y")
	seed.Dir, seed.Env = dir, env
	if out, err := seed.CombinedOutput(); err != nil {
		t.Fatalf("seeding D1: %v\n%s", err, out)
	}

	port, inspector := freePort(t), freePort(t)
	var logs syncBuffer
	dev := exec.Command(wrangler, "dev", "--local", "--persist-to", state,
		"--ip", "127.0.0.1", "--port", fmt.Sprint(port), "--inspector-port", fmt.Sprint(inspector),
		"--show-interactive-dev-session=false")
	dev.Dir, dev.Env = dir, env
	dev.Stdout, dev.Stderr = &logs, &logs
	dev.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := dev.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-dev.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = dev.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-dev.Process.Pid, syscall.SIGKILL)
		}
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	for {
		if strings.Contains(logs.String(), "Ready on") {
			if r, err := http.Get(base + "/"); err == nil {
				r.Body.Close()
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("wrangler dev did not start:\n%s", logs.String())
		}
		time.Sleep(200 * time.Millisecond)
	}

	out, err := exec.Command(node, "workers/bank.workers.mjs", base).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("%v\n--- wrangler output ---\n%s", err, logs.String())
	}

	// Outbox entries were delivered (logged) after each committed transfer,
	// and never for rolled-back or conflicting ones.
	time.Sleep(300 * time.Millisecond)
	l := logs.String()
	t.Logf("--- wrangler output ---\n%s", l)
	const entry = `kekkai outbox: POST https://hooks.example/transfer "alice->bob:30"`
	if n := strings.Count(l, entry); n != 2 { // one per backend
		t.Errorf("expected the first transfer's outbox entry twice, found %d\n%s", n, l)
	}
	if strings.Contains(l, `"alice->bob:5"`) {
		t.Errorf("outbox entry of a conflicting transaction was delivered\n%s", l)
	}
	if strings.Contains(l, "handler failed") {
		t.Errorf("handler failures in wrangler output\n%s", l)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
