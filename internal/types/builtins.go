package types

// Builtin describes a builtin method or static function. Every builtin
// whose receiver is a capability performs an effect; all other builtins are
// pure.
type Builtin struct {
	Recv   string // receiver type name ("Log", "Tx", "String", ...) or static owner ("Response")
	Name   string
	Params []Type
	Result Type
	// Async builtins suspend the computation (they return a JS promise).
	// Functions that can reach one are compiled to resumable state machines.
	Async bool
	// Consumes means the call consumes the (linear) receiver.
	Consumes bool
	// Op is the operation name in the IR.
	Op string
}

func res(ok, err Type) Type { return &ResultT{ok, err} }
func opt(t Type) Type       { return &OptionT{t} }

var methods = map[string]map[string]*Builtin{}
var statics = map[string]map[string]*Builtin{}

func def(b *Builtin, static bool) {
	tbl := methods
	if static {
		tbl = statics
	}
	if tbl[b.Recv] == nil {
		tbl[b.Recv] = map[string]*Builtin{}
	}
	if b.Op == "" {
		b.Op = lower(b.Recv) + "." + b.Name
	}
	tbl[b.Recv][b.Name] = b
}

func lower(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'A' && b[0] <= 'Z' {
		b[0] += 'a' - 'A'
	}
	return string(b)
}

func init() {
	m := func(b *Builtin) { def(b, false) }
	s := func(b *Builtin) { def(b, true) }

	// ---- capabilities ----
	m(&Builtin{Recv: "Log", Name: "info", Params: []Type{String}, Result: Unit})
	m(&Builtin{Recv: "Log", Name: "warn", Params: []Type{String}, Result: Unit})
	m(&Builtin{Recv: "Log", Name: "error", Params: []Type{String}, Result: Unit})

	m(&Builtin{Recv: "Clock", Name: "now_ms", Result: Int})
	m(&Builtin{Recv: "Random", Name: "int", Params: []Type{Int, Int}, Result: Int})

	m(&Builtin{Recv: "Net", Name: "get", Params: []Type{String}, Result: res(String, NetError), Async: true})
	m(&Builtin{Recv: "Net", Name: "post", Params: []Type{String, String}, Result: res(String, NetError), Async: true})

	// Db is an abstract transactional key-value store. The language fixes
	// only the transaction protocol (linear Tx, no irrevocable effects
	// inside, outbox, automatic rollback); the backend (Durable Objects
	// storage, D1, a distributed KV, an in-memory store for tests) is an
	// adapter chosen by the runtime. Db.transaction is special-cased by the
	// checker.
	m(&Builtin{Recv: "Db", Name: "get", Params: []Type{String}, Result: res(opt(String), TxError), Async: true})

	m(&Builtin{Recv: "Tx", Name: "get", Params: []Type{String}, Result: res(opt(String), TxError), Async: true})
	m(&Builtin{Recv: "Tx", Name: "put", Params: []Type{String, String}, Result: res(Unit, TxError), Async: true})
	m(&Builtin{Recv: "Tx", Name: "delete", Params: []Type{String}, Result: res(Unit, TxError), Async: true})
	m(&Builtin{Recv: "Tx", Name: "outbox", Params: []Type{String, String}, Result: Unit})
	m(&Builtin{Recv: "Tx", Name: "commit", Result: res(Unit, TxError), Async: true, Consumes: true})
	m(&Builtin{Recv: "Tx", Name: "rollback", Result: Unit, Consumes: true})

	// Fs is the file system (used by command-line programs such as the
	// self-hosted compiler). Writes are irrevocable.
	m(&Builtin{Recv: "Fs", Name: "read", Params: []Type{String}, Result: res(String, IoError), Async: true})
	m(&Builtin{Recv: "Fs", Name: "write", Params: []Type{String, String}, Result: res(Unit, IoError), Async: true})
	m(&Builtin{Recv: "Fs", Name: "write_bytes", Params: []Type{String, &VecT{Int}}, Result: res(Unit, IoError), Async: true})

	// ---- pure data ----
	m(&Builtin{Recv: "Int", Name: "to_string", Result: String})
	m(&Builtin{Recv: "Int", Name: "abs", Result: Int})
	for _, op := range []string{"bit_and", "bit_or", "bit_xor", "shl", "shr", "ushr", "min", "max"} {
		m(&Builtin{Recv: "Int", Name: op, Params: []Type{Int}, Result: Int})
	}
	m(&Builtin{Recv: "Bool", Name: "to_string", Result: String})

	m(&Builtin{Recv: "String", Name: "len", Result: Int})
	m(&Builtin{Recv: "String", Name: "parse_int", Result: opt(Int)})
	m(&Builtin{Recv: "String", Name: "contains", Params: []Type{String}, Result: Bool})
	m(&Builtin{Recv: "String", Name: "starts_with", Params: []Type{String}, Result: Bool})
	m(&Builtin{Recv: "String", Name: "ends_with", Params: []Type{String}, Result: Bool})
	m(&Builtin{Recv: "String", Name: "trim", Result: String})
	m(&Builtin{Recv: "String", Name: "to_upper", Result: String})
	m(&Builtin{Recv: "String", Name: "to_lower", Result: String})
	m(&Builtin{Recv: "String", Name: "char_at", Params: []Type{Int}, Result: opt(Int)})
	m(&Builtin{Recv: "String", Name: "slice", Params: []Type{Int, Int}, Result: String})
	m(&Builtin{Recv: "String", Name: "index_of", Params: []Type{String}, Result: opt(Int)})
	m(&Builtin{Recv: "String", Name: "replace", Params: []Type{String, String}, Result: String})
	m(&Builtin{Recv: "String", Name: "split", Params: []Type{String}, Result: &VecT{String}})
	m(&Builtin{Recv: "String", Name: "to_bytes", Result: &VecT{Int}})
	s(&Builtin{Recv: "String", Name: "from_char", Params: []Type{Int}, Result: String})
	s(&Builtin{Recv: "String", Name: "from_bytes", Params: []Type{&VecT{Int}}, Result: String})
	m(&Builtin{Recv: "IoError", Name: "message", Result: String})

	m(&Builtin{Recv: "Request", Name: "method", Result: String})
	m(&Builtin{Recv: "Request", Name: "path", Result: String})
	m(&Builtin{Recv: "Request", Name: "segment", Params: []Type{Int}, Result: opt(String)})
	m(&Builtin{Recv: "Request", Name: "query", Params: []Type{String}, Result: opt(String)})
	m(&Builtin{Recv: "Request", Name: "header", Params: []Type{String}, Result: opt(String)})
	m(&Builtin{Recv: "Request", Name: "body", Result: String})

	m(&Builtin{Recv: "Response", Name: "with_header", Params: []Type{String, String}, Result: Response})

	m(&Builtin{Recv: "TxError", Name: "message", Result: String})
	// retryable is true for optimistic-concurrency conflicts: re-running
	// the whole transaction may succeed.
	m(&Builtin{Recv: "TxError", Name: "retryable", Result: Bool})
	m(&Builtin{Recv: "NetError", Name: "message", Result: String})

	s(&Builtin{Recv: "Response", Name: "text", Params: []Type{Int, String}, Result: Response})
	s(&Builtin{Recv: "Response", Name: "json", Params: []Type{Int, String}, Result: Response})
	s(&Builtin{Recv: "Response", Name: "empty", Params: []Type{Int}, Result: Response})
	s(&Builtin{Recv: "Response", Name: "no_content", Result: Response})
	s(&Builtin{Recv: "Response", Name: "not_found", Result: Response})
	s(&Builtin{Recv: "Response", Name: "bad_request", Params: []Type{String}, Result: Response})
}

// LookupMethod finds a builtin method for a receiver type name.
func LookupMethod(recv, name string) *Builtin { return methods[recv][name] }

// LookupStatic finds a builtin static function `Owner::name`.
func LookupStatic(owner, name string) *Builtin { return statics[owner][name] }

// AllBuiltins returns every builtin (for documentation and glue generation).
func AllBuiltins() []*Builtin {
	var out []*Builtin
	for _, tbl := range []map[string]map[string]*Builtin{methods, statics} {
		for _, ms := range tbl {
			for _, b := range ms {
				out = append(out, b)
			}
		}
	}
	return out
}
