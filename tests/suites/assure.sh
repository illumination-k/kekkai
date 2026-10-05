#!/bin/sh
# assure: the guarantee ledger `kek assure plan|apply|check` on the fixture
# testdata/assure (a program app/, its policy kekkai.toml and its approved
# lock kekkai.assure.lock). Every case copies the fixture to a temporary
# directory, edits the copy, runs a sequence of commands there and compares
# the transcript with tests/assure/golden/<case>.txt.
#
#   UPDATE=1 tests/run.sh assure    rewrite the golden files
#
# The date is fixed with KEK_TODAY (the launcher passes it as -today).
. "$(dirname "$0")/../lib.sh"
SUITE=assure
golden=$ROOT/tests/assure/golden
fixture=$ROOT/testdata/assure
# the commands run outside the repository, where a mise shim has no version
if [ -z "${WASMTIME:-}" ] && command -v mise >/dev/null 2>&1; then
	WASMTIME=$(mise which wasmtime 2>/dev/null || echo wasmtime)
	export WASMTIME
fi

# step <args...>: run `kek <args>` in the case directory and append the
# transcript.
step() {
	echo "\$ kek $*" >>"$d/got"
	set +e
	(cd "$d/w" && "$KEK" "$@" >"$d/out" 2>"$d/err")
	_c=$?
	set -e
	cat "$d/out" >>"$d/got"
	if [ -s "$d/err" ]; then
		echo "--- stderr" >>"$d/got"
		cat "$d/err" >>"$d/got"
	fi
	echo "--- exit $_c" >>"$d/got"
	echo >>"$d/got"
}

# note <text>: a comment in the transcript.
note() {
	echo "# $*" >>"$d/got"
}

# edit <file> <sed expression>
edit() {
	sed "$2" "$d/w/$1" >"$d/w/$1.tmp" && mv "$d/w/$1.tmp" "$d/w/$1"
}

lock_excerpt() {
	note "waivers in the lock:"
	grep -A 12 '"waivers": \[$' "$d/w/kekkai.assure.lock" | grep -v '^ *"waivers": \[\]' >>"$d/got" || true
	echo >>"$d/got"
}

case_initial() {
	rm "$d/w/kekkai.assure.lock"
	step assure check app
	step assure plan app
	note "new assumptions need review"
	step assure apply app
	step assure apply -yes app
	step assure check app
	if cmp -s "$d/w/kekkai.assure.lock" "$fixture/kekkai.assure.lock"; then
		note "the lock equals testdata/assure/kekkai.assure.lock"
	else
		note "the lock differs from testdata/assure/kekkai.assure.lock:"
		diff "$fixture/kekkai.assure.lock" "$d/w/kekkai.assure.lock" >>"$d/got" || true
	fi
	step assure apply app
}

case_no_changes() {
	step assure plan app
	step assure check app
	step assure check -json app
}

case_strengthen() {
	note "fetch_rate no longer takes &Log"
	edit app/rates.kek 's/fn fetch_rate(net: &Net, log: &Log, cur: String)/fn fetch_rate(net: \&Net, cur: String)/'
	edit app/rates.kek 's/            log.warn(e.message());//'
	edit app/rates.kek 's/fetch_rate(net, log, cur)/fetch_rate(net, cur)/'
	step assure check app
	step assure plan -v app
	step assure plan -json app
	step assure apply app
	step assure check app
}

case_weaken_capability() {
	note "rate_key starts logging: it gains &Log"
	edit app/rates.kek 's/fn rate_key(cur: String) -> String {/fn rate_key(log: \&Log, cur: String) -> String {\
    log.info(cur);/'
	step assure plan app
	step assure plan -json app
	step assure check app
	step assure apply app
	step assure apply -yes app
	step assure apply -yes -reason "audit log" -owner shogo app
	step assure apply -yes -reason "audit log" -owner shogo -expires 2026-01-01 app
	step assure apply -yes -reason "audit log" -owner shogo -expires 2027-01-31 app
	lock_excerpt
	step assure check app
	note "a month after the waiver expired"
	KEK_TODAY=2027-02-01
	export KEK_TODAY
	step assure plan app
	step assure plan -strict app
	step assure check app
	step assure apply -renew app
	step assure apply -renew -reason "audit log, still needed" -owner shogo -expires 2027-06-30 app
	lock_excerpt
	step assure check app
	note "rate_key stops logging: the waiver goes away"
	edit app/rates.kek 's/fn rate_key(log: &Log, cur: String)/fn rate_key(cur: String)/'
	edit app/rates.kek '/    log.info(cur);/d'
	step assure plan app
	step assure apply app
	step assure check app
}

case_allowed_host() {
	note "fetch_rate moves to another host of allowed_hosts"
	edit app/rates.kek 's|https://api.rates.example/v1/|https://hooks.warehouse.example/rates/|'
	step assure plan -v app
	step assure apply app
	step assure check app
}

case_forbidden_host() {
	note "fetch_rate contacts a host outside allowed_hosts"
	edit app/rates.kek 's|https://api.rates.example/v1/|https://rates.evil.example/v1/|'
	step assure plan app
	step assure plan -json app
	step assure apply -yes -reason r -owner o -expires 2027-01-01 app
	step assure check app
}

case_unknown_host() {
	note "the URL is not a literal: the host is unknown (*)"
	edit app/rates.kek 's|net.get("https://api.rates.example/v1/" + cur)|net.get(cur)|'
	step assure plan app
	step assure check app
}

case_weaken_host_no_allowlist() {
	note "without [net] allowed_hosts a new host is a weakening that needs review"
	cat >"$d/w/open.toml" <<'EOF'
[escalate]
weaken = "security"

[assure]
lock = "open.lock"
require = ["reason", "owner"]
EOF
	step assure apply -yes -config open.toml app
	edit app/rates.kek 's|https://api.rates.example/v1/|https://rates.other.example/v1/|'
	step assure plan -config open.toml app
	step assure apply -yes -config open.toml app
	step assure apply -yes -config open.toml -reason "new provider" -owner shogo app
	step assure check -config open.toml app
	note "the waiver in open.lock (no expiry required by this policy):"
	grep -A 9 '"waivers": \[$' "$d/w/open.lock" >>"$d/got" || true
}

case_forbidden_capability() {
	note "a function in app/shop.kek takes &Random (forbidden there)"
	cat >>"$d/w/app/shop.kek" <<'EOF'

fn discount(random: &Random) -> Int {
    random.int(0, 10)
}
EOF
	step assure plan app
	step assure apply -yes app
	step assure check app
	note "the same function in app/rates.kek is allowed"
	tail -n 4 "$d/w/app/shop.kek" >>"$d/w/app/rates.kek"
	edit app/shop.kek '/^fn discount/,$d'
	step assure plan app
	step assure apply app
	step assure check app
}

case_expired_assumption() {
	note "the #[allow(similar)] on rate_key expires 2027-03-31"
	KEK_TODAY=2027-04-01
	export KEK_TODAY
	step assure plan app
	step assure plan -strict app
	step assure check app
	step assure check -json app
	note "extending it in the code is a changed assumption (needs review)"
	edit app/rates.kek 's/expires = "2027-03-31"/expires = "2027-09-30"/'
	step assure plan app
	step assure apply -yes app
	step assure check app
	note "a malformed date is a violation"
	edit app/rates.kek 's/expires = "2027-09-30"/expires = "next year"/'
	step assure plan app
}

case_tested_lost() {
	note "the only #[test] is removed: parse_price loses its test evidence"
	edit app/shop.kek '/^#\[test\]/,$d'
	step assure plan app
	step assure apply -yes -reason "test moves to the next PR" -owner shogo -expires 2026-11-01 app
	step assure check app
}

case_tx_and_body() {
	note "buy stops opening a transaction; parse_price changes only its body"
	cat >"$d/w/app/shop.kek" <<'EOF'
fn price_key(item: String) -> String {
    "price:" + item
}

fn parse_price(s: Option<String>) -> Int {
    match s {
        Some(text) => text.trim().parse_int().unwrap_or(0),
        None => 0,
    }
}

fn buy(db: &Db, log: &Log, item: String) -> Result<Int, TxError> {
    let p = parse_price(db.get(price_key(item))?);
    log.info("sold " + item);
    Ok(p)
}

#[handler]
fn handle(req: Request, db: &Db, log: &Log) -> Response {
    let item = req.segment(0).unwrap_or("");
    match buy(db, log, item) {
        Ok(p) => Response::text(200, p.to_string()),
        Err(e) => Response::text(500, e.message()),
    }
}

#[test]
fn price_defaults_to_zero() -> Bool {
    parse_price(None) == 0 && parse_price(Some("7")) == 7
}
EOF
	step assure plan -v app
	step assure apply app
	step assure check app
}

case_module_review() {
	note "app/rates.kek requires review of every change; rate_key is renamed (same hash)"
	cat >>"$d/w/kekkai.toml" <<'EOF'

[module."app/rates.kek"]
review = ["change", "added", "removed"]
EOF
	edit app/rates.kek 's/amount \* fetch_rate/fetch_rate/'
	edit app/rates.kek 's/^fn rate_key/fn currency_key/'
	step assure plan app
	step assure apply app
	step assure apply -yes app
	step assure check app
}

case_config_errors() {
	note "contradictory and malformed policies"
	cat >"$d/w/bad.toml" <<'EOF'
[auto_approve]
weaken = true
new_assumption = true
bogus = true

[escalate]
new_assumption = "security"
change = "core"

[effects]
forbid = ["Network"]

[module."app/rates.kek"]
allowed_hosts = ["api.rates.example", "elsewhere.example"]
forbid = ["Net"]

[module."app"]
allowed_hosts = ["api.rates.example"]

[assure]
require = ["reason", "signature"]
EOF
	cat >"$d/w/bad_net.toml" <<'EOF'
[net]
allowed_hosts = ["api.rates.example"]

[effects]
forbid = ["Net"]
EOF
	step assure plan -config bad.toml app
	step assure check -config bad_net.toml app
	step assure apply -yes -config bad.toml app
	step assure plan -config missing.toml app
}

case_extends() {
	note "kekkai.toml extends an organization policy and may only tighten it"
	cat >"$d/w/org.toml" <<'EOF'
[net]
allowed_hosts = ["api.rates.example", "hooks.warehouse.example"]

[auto_approve]
change = false

[effects]
forbid = ["Fs"]
EOF
	cat >"$d/w/kekkai.toml" <<'EOF'
[assure]
extends = "org.toml"

[net]
allowed_hosts = ["api.rates.example", "hooks.warehouse.example"]

[module."app/shop.kek"]
forbid = ["Random"]
EOF
	note "the organization policy adds rules: the policy hash changes"
	step assure plan app
	note "loosening the parent is an error"
	cat >"$d/w/kekkai.toml" <<'EOF'
[assure]
extends = "org.toml"

[net]
allowed_hosts = ["api.rates.example", "hooks.warehouse.example", "more.example"]

[auto_approve]
change = true
EOF
	step assure plan app
	note "tightening it is fine; the parent's [auto_approve] change = false applies"
	cat >"$d/w/kekkai.toml" <<'EOF'
[assure]
extends = "org.toml"

[net]
allowed_hosts = ["api.rates.example"]
EOF
	step assure plan app
}

case_idempotent_lost() {
	note "fetch_rate starts posting: it, and convert that calls it, are no longer idempotent"
	edit app/rates.kek 's|net.get("https://api.rates.example/v1/" + cur)|net.post("https://api.rates.example/v1/" + cur, "")|'
	step assure plan app
	step assure plan -json app
	step assure check app
	step assure apply -yes -reason "the rates API wants a POST" -owner shogo -expires 2027-01-31 app
	step assure check app
	note "convert stops calling it (pure now): no weakening of idempotent, its effects strengthen"
	edit app/rates.kek 's/    amount \* fetch_rate(net, log, cur)/    amount/'
	edit app/rates.kek 's/^fn convert(net: &Net, log: &Log, amount: Int, cur: String)/fn convert(amount: Int, cur: String)/'
	step assure plan -v app
	note "#[handler(idempotent)] on handle does not compile: buy queues an outbox message"
	edit app/shop.kek 's/^#\[handler\]$/#[handler(idempotent)]/'
	step assure plan app
}

case_refine() {
	note "refinement proofs: the indexes, divisions and arithmetic of a function are all proved"
	cat >"$d/w/app/stats.kek" <<'EOF'
fn largest_share(v: Vec<Int>) -> Int {
    let mut best = 0;
    let mut i = 0;
    while i < v.len() {
        if v[i] > best {
            best = v[i];
        }
        i = i + 1;
    }
    if v.len() == 0 {
        return 0;
    }
    best / v.len()
}
EOF
	step assure plan app
	step assure apply -yes app
	grep -A 30 '"largest_share": {' "$d/w/kekkai.assure.lock" | sed '/"assumptions"/q' >>"$d/got"
	echo >>"$d/got"
	note "an off-by-one loop does not type-check: the index is out of bounds for i == v.len()"
	edit app/stats.kek 's/while i < v.len()/while i <= v.len()/'
	step assure plan app
	note "best + 1 may overflow: refine.no_overflow is lost (a weakening)"
	edit app/stats.kek 's/while i <= v.len()/while i < v.len()/'
	edit app/stats.kek 's|    best / v.len()|    (best + 1) / v.len()|'
	step assure plan app
	step assure check app
}

case_pii() {
	note "notify_rate declassifies personal data twice: new assumptions"
	cat >>"$d/w/app/rates.kek" <<'EOF'

fn notify_rate(log: &Log, email: Labeled<PII, String>, cur: String) {
    log.info("rate " + cur + " for " + email.mask());
    log.info("user " + email.hash());
}
EOF
	step assure plan app
	step assure plan -json app
	step assure apply -yes app
	note "the assumptions in the lock:"
	grep -B 1 -A 4 '"kind": "flow.declassify"' "$d/w/kekkai.assure.lock" >>"$d/got"
	echo >>"$d/got"
	step assure check app
	note "[flow]: at most one declassification per module, approved with a reason and an owner"
	cat >>"$d/w/kekkai.toml" <<'EOF'

[flow]
max_declassify_per_module = 1
declassify_requires = ["reason", "owner"]
EOF
	step assure plan app
	step assure plan -json app
	step assure check app
	note "notify_rate names who approved it and keeps one declassification"
	edit app/rates.kek 's/^fn notify_rate/#[declassify(reason = "support sees the domain", owner = "shogo", expires = "2027-06-30")]\
fn notify_rate/'
	edit app/rates.kek '/email.hash()/d'
	step assure plan app
	step assure apply -yes app
	step assure check app
	note "it expires"
	KEK_TODAY=2027-07-01
	export KEK_TODAY
	step assure check app
	note "malformed [flow] settings, and loosening the limit of a parent ([pii] is its old name)"
	cat >"$d/w/bad.toml" <<'EOF'
[flow]
max_declassify_per_module = "two"
declassify_requires = ["reason", "signature"]
mask = true
EOF
	step assure plan -config bad.toml app
	cat >"$d/w/org.toml" <<'EOF'
[pii]
max_declassify_per_module = 1
declassify_requires = ["owner"]
EOF
	cat >"$d/w/child.toml" <<'EOF'
[assure]
extends = "org.toml"

[pii]
max_declassify_per_module = 3
declassify_requires = ["expires"]
EOF
	step assure plan -config child.toml app
}

case_authz() {
	note "a policy grants Edit on a document; retitle requires it; title_len keeps the title labeled"
	cat >"$d/w/app/docs.kek" <<'EOF'
struct Edit {}

struct Doc {
    id: Int,
    owner: Int,
    title: Labeled<PII, String>,
}

#[policy(reason = "owners edit their documents", owner = "shogo")]
fn can_edit(user: Int, d: Doc) -> Option<Can<Edit, d>> {
    if d.owner == user { Some(Can::grant()) } else { None }
}

fn retitle(d: Doc, t: Labeled<PII, String>, _cap: Can<Edit, d>) -> Doc {
    Doc { id: d.id, owner: d.owner, title: t }
}

fn title_len(d: Doc) -> Labeled<PII, Int> {
    d.title.map(|t| t.len())
}
EOF
	step assure plan app
	step assure apply -yes app
	grep -A 22 '"retitle": {' "$d/w/kekkai.assure.lock" | sed '/"assumptions"/q' >>"$d/got"
	echo >>"$d/got"
	note "title_len starts to declassify: flow.noninterference is lost (a weakening)"
	edit app/docs.kek 's/d.title.map(|t| t.len())/PII::label(d.title.mask().len())/'
	step assure plan app
	note "retitle no longer requires the permission (a weakening)"
	edit app/docs.kek 's/, _cap: Can<Edit, d>) -> Doc/) -> Doc/'
	step assure plan app
}

case_usage() {
	step assure
	step assure plan
	step assure frobnicate app
	step assure plan -today 2026-13-01 app
	step assure plan -bogus app
	step assure plan nonexistent.kek
	echo '{"version": 2, "config": "", "definitions": {}}' >"$d/w/kekkai.assure.lock"
	step assure plan app
}

cases="initial no_changes strengthen weaken_capability allowed_host forbidden_host unknown_host
weaken_host_no_allowlist forbidden_capability expired_assumption tested_lost tx_and_body module_review
config_errors extends idempotent_lost pii refine authz usage"

one() {
	name=$1
	d=$(tmpdir)
	mkdir "$d/w"
	cp -R "$fixture/." "$d/w/"
	: >"$d/got"
	KEK_TODAY=2026-10-04
	export KEK_TODAY
	"case_$name"
	if [ "${UPDATE:-}" = 1 ]; then
		mkdir -p "$golden"
		cp "$d/got" "$golden/$name.txt"
		case_ok "$name" "updated"
	elif [ ! -f "$golden/$name.txt" ]; then
		case_fail "$name" "missing golden file tests/assure/golden/$name.txt (UPDATE=1 to create)"
	elif cmp -s "$d/got" "$golden/$name.txt"; then
		case_ok "$name"
	else
		case_fail "$name" "output differs from tests/assure/golden/$name.txt:
$(diff "$golden/$name.txt" "$d/got")"
	fi
	rm -rf "$d"
}

if [ "${1:-}" = --case ]; then
	one "$2"
	exit 0
fi
# shellcheck disable=SC2086
run_parallel "$0" $cases
