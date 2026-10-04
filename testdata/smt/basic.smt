// Pure bounds, equalities, `!=`, disjunctions in hypotheses.

problem bounds_valid
hyp h1: x <= 5
hyp h2: x >= 3
goal: 0 < x && x < 10

problem bounds_conflict
hyp h1: x <= 2
hyp h2: x >= 3
hyp h3: y == 7
goal: false

problem bounds_invalid
hyp h1: 0 <= x <= 10
goal: x <= 9

problem trivially_true_goal
hyp h1: x <= 5
goal: 1 <= 2

problem trivially_false_goal
goal: 2 <= 1

problem distinct_vars_intervals
hyp a: 0 <= x <= 3
hyp b: 5 <= y <= 9
hyp c: -2 <= z
goal: x < y

problem eq_subst
hyp h1: y == x + 2
hyp h2: z == y + 3
hyp h3: w == 100
goal: z == x + 5

problem eq_chain_invalid
hyp h1: y == x + 2
hyp h2: z == 2*y
goal: z == 2*x + 3

problem neq_goal_valid
hyp h1: x == 5
goal: x != 3

problem neq_goal_invalid
hyp h1: x >= 3
hyp h2: x <= 4
goal: x != 3

problem neq_hyp
hyp h1: 0 <= x <= 1
hyp h2: x != 0
goal: x == 1

problem neq_hyp_split
hyp h1: x != y
hyp h2: y == 4
goal: x <= 3 || x >= 5

problem or_hyp_valid
hyp h1: x <= -1 || x >= 10
hyp h2: x >= 0
goal: x >= 10

problem or_hyp_invalid
hyp h1: x <= -1 || x >= 10
goal: x >= 10

problem or_both_branches
hyp h1: x == 1 || x == 2
hyp h2: y == 2*x
goal: 2 <= y <= 4

problem nested_bool
hyp h1: !(x > 3 && y > 3)
hyp h2: x >= 5
goal: y <= 3

problem not_or
hyp h1: !(x < 0 || x > 9)
goal: 0 <= x && x <= 9

problem strict_to_nonstrict
hyp h1: x < y
goal: x + 1 <= y

problem three_disjunctions
hyp h1: a == 0 || a == 1
hyp h2: b == 0 || b == 1
hyp h3: c == 0 || c == 1
goal: 0 <= a + b + c <= 3

problem three_disjunctions_invalid
hyp h1: a == 0 || a == 1
hyp h2: b == 0 || b == 1
hyp h3: c == 0 || c == 1
goal: a + b + c <= 2

problem transitivity
hyp h1: x <= y
hyp h2: y <= z
hyp h3: z <= w
goal: x <= w

problem unconstrained_atom
hyp h1: x <= 5
goal: x + y <= 5

problem parenthesized_terms
hyp h1: 2*(x + 1) <= (y - 3) * 2
goal: x + 4 <= y
