// Coefficients that grow past 64 bits during elimination give
// `unknown: overflow` rather than a wrapped (wrong) answer; bounds near
// the limits that need no growth still work.

problem huge_combination
hyp h1: 4611686018427387904*x + 3*y <= 0
hyp h2: -3*x + 4611686018427387905*y <= 5
hyp h3: -5*x - 7*y <= 1
goal: false

problem constant_overflow_in_negation
// !(x >= MIN) is x <= MIN - 1, whose constant does not fit: it is weakened
// to x <= -MAX, and the model x = -MAX does not refute the goal; the real
// counterexample x = MIN - 1 is no Int
hyp h1: x <= 9223372036854775807
goal: x >= -9223372036854775807 - 1

problem big_bounds
hyp h1: x <= 9223372036854775807
hyp h2: x >= 9223372036854775806
goal: x >= 9223372036854775806

problem big_equality_substitution
hyp h1: y == 3037000500*x
hyp h2: z == 3037000500*y
hyp h3: 1 <= x
goal: z >= 0

problem model_out_of_range
hyp h1: x >= 9223372036854775807
hyp h2: y >= x
goal: y <= 0
