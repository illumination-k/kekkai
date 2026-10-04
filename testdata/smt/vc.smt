// Verification conditions shaped like the type checker's: index safety,
// loop counters, overflow checks, unsat cores, models.

problem index_safe
hyp lo: 0 <= i
hyp hi: i < n
hyp len: n == len(v)
hyp other: k >= 100
goal: 0 <= i && i < len(v)

problem index_unsafe_off_by_one
hyp lo: 0 <= i
hyp hi: i <= n
hyp len: n == len(v)
goal: i < len(v)

problem index_negative
hyp hi: i < len(v)
hyp len_nonneg: len(v) >= 0
goal: 0 <= i && i < len(v)

problem loop_counter
// let mut i = 0; ... i = i + 1 (a new version per assignment)
hyp init: i#0 == 0
hyp inv: i#1 >= i#0
hyp step: i#2 == i#1 + 1
goal: i#2 >= 0

problem loop_counter_from_param
hyp start: i0 >= 0
hyp step: i == i0 + 1
goal: i >= 0

problem for_range
hyp range_lo: a <= i
hyp range_hi: i < b
hyp b_le_len: b <= v.len()
hyp a_nonneg: 0 <= a
goal: 0 <= i && i < v.len()

problem midpoint
hyp h1: 0 <= lo
hyp h2: lo < hi
hyp h3: hi <= len(v)
hyp mid: 2*mid <= lo + hi && lo + hi <= 2*mid + 1
goal: 0 <= mid && mid < len(v)

problem clamp_post
hyp pre: lo <= hi
hyp branch: (x < lo && r == lo) || (x > hi && r == hi) || (lo <= x && x <= hi && r == x)
goal: lo <= r && r <= hi

problem clamp_post_wrong
hyp branch: (x < lo && r == lo) || (x > hi && r == hi) || (lo <= x && x <= hi && r == x)
goal: lo <= r && r <= hi

problem add_no_overflow
hyp a: 0 <= x < 1000
hyp b: 0 <= y < 1000
goal: x + y <= 9223372036854775807 && x + y >= -9223372036854775807 - 1

problem add_may_overflow
hyp a: x >= 0
hyp b: y >= 0
goal: x + y <= 9223372036854775807

problem div_nonzero
hyp h1: d >= 1 || d <= -1
goal: d != 0

problem port
hyp port: 0 < p && p < 65536
hyp lt: p < 1024
goal: 0 < p + 1024 && p + 1024 < 65536

problem core_subset
hyp a: x >= 10
hyp b: y >= 20
hyp c: z == 3
hyp d: x <= 20
goal: x > 5

problem core_from_disjunction
hyp a: x == 1 || x == 2
hyp b: y == 7
hyp c: x + 1 <= 9
goal: x <= 2

problem unnamed_hyps
hyp: 0 <= i
hyp named: i < n
goal: i != n

problem model_three_atoms
hyp a: x + y + z == 10
hyp b: x >= y
hyp c: y >= z
goal: z >= 2
