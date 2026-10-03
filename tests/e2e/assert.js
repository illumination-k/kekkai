// Assertions for the e2e scenarios: equal (Object.is), deepEqual
// (structural, including Map, Set and arrays), ok and rejects. Failures
// throw an AssertionError with both values.
export class AssertionError extends Error {
  constructor(message) {
    super(message);
    this.name = "AssertionError";
  }
}

function show(v) {
  try {
    return JSON.stringify(v, (_, x) =>
      typeof x === "bigint" ? `${x}n` : x instanceof Map ? { Map: [...x] } : x instanceof Set ? { Set: [...x] } : x);
  } catch {
    return String(v);
  }
}

function fail(message, actual, expected, op) {
  throw new AssertionError(message ?? `expected ${show(actual)} ${op} ${show(expected)}`);
}

function deep(a, b) {
  if (Object.is(a, b)) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false;
  if (Object.getPrototypeOf(a) !== Object.getPrototypeOf(b)) return false;
  if (a instanceof Map) {
    if (a.size !== b.size) return false;
    for (const [k, v] of a) if (!b.has(k) || !deep(v, b.get(k))) return false;
    return true;
  }
  if (a instanceof Set) {
    if (a.size !== b.size) return false;
    for (const v of a) if (!b.has(v)) return false;
    return true;
  }
  if (a instanceof Date) return a.getTime() === b.getTime();
  const ka = Object.keys(a);
  const kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  for (const k of ka) if (!Object.prototype.hasOwnProperty.call(b, k) || !deep(a[k], b[k])) return false;
  return true;
}

function matches(e, expected) {
  if (expected === undefined) return true;
  if (typeof expected === "function") return expected.prototype !== undefined && e instanceof expected ? true : expected(e) === true;
  if (expected instanceof RegExp) return expected.test(String(e && e.message !== undefined ? e.message : e));
  if (typeof expected === "object") {
    for (const [k, v] of Object.entries(expected)) {
      const got = e?.[k];
      if (v instanceof RegExp ? !v.test(String(got)) : !deep(got, v)) return false;
    }
    return true;
  }
  return false;
}

const assert = {
  equal(actual, expected, message) {
    if (!Object.is(actual, expected)) fail(message, actual, expected, "===");
  },
  deepEqual(actual, expected, message) {
    if (!deep(actual, expected)) fail(message, actual, expected, "deep-equal to");
  },
  ok(value, message) {
    if (!value) throw new AssertionError(message ?? `expected a truthy value, got ${show(value)}`);
  },
  async rejects(promiseOrFn, expected, message) {
    if (typeof expected === "string") {
      message = expected;
      expected = undefined;
    }
    try {
      await (typeof promiseOrFn === "function" ? promiseOrFn() : promiseOrFn);
    } catch (e) {
      if (!matches(e, expected)) throw new AssertionError(message ?? `rejected with an unexpected error: ${e && e.message}`);
      return;
    }
    throw new AssertionError(message ?? "expected a rejection");
  },
};

export default assert;
