// Package js holds the JavaScript side of Kekkai (the runtime that hosts
// compiled WasmGC modules, and the Node launchers). The files are the
// source of truth; Go embeds them only while the Go toolchain exists.
package js

import _ "embed"

//go:embed kekkai_runtime.js
var Runtime string

//go:embed run.mjs
var RunMJS string

//go:embed test_runner.mjs
var TestRunner string
