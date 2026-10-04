// The text syntax: atoms, chains, parentheses, comments; errors are
// reported per problem (exit status 1).

hyp: 0 <= x#2 < len(v) <= 10
goal: x#2 <= 8

problem dotted_atoms
hyp h: p.x + v.len() >= 3 && p.x <= 1
goal: v.len() >= 2

problem nonlinear
hyp h: x * y <= 3
goal: true

problem bad_token
hyp h: x <= 3 ;
goal: true

problem missing_comparison
goal: x + 1

problem unknown_line
assume: x <= 1

problem bad_number
goal: x <= 99999999999999999999

problem default_goal_false
hyp h1: x >= 1
hyp h2: x <= 0
