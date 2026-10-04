// Unsatisfiable over the integers but not over the rationals: gcd
// tightening, equalities without a unit coefficient (Pugh's mod^ step),
// the dark shadow and splinters.

problem two_x_eq_one
goal: 2*x != 1

problem one_le_3x_le_2
hyp h1: 1 <= 3*x
hyp h2: 3*x <= 2
goal: false

problem even_odd
hyp h1: x == 2*y
hyp h2: x == 2*z + 1
goal: false

problem gcd_eq
hyp h1: 6*x + 9*y == 4
goal: false

problem gcd_eq_sat
hyp h1: 6*x + 9*y == 3
goal: false

problem modhat_eq
// 7x + 12y + 31z == 17, 3x + 5y + 14z == 7 (Pugh's example) has solutions
hyp h1: 7*x + 12*y + 31*z == 17
hyp h2: 3*x + 5*y + 14*z == 7
hyp h3: 1 <= x <= 40
hyp h4: -50 <= y <= 50
goal: false

problem modhat_eq_bounded_unsat
hyp h1: 7*x + 12*y + 31*z == 17
hyp h2: 3*x + 5*y + 14*z == 7
hyp h3: 0 <= x <= 5
hyp h4: 0 <= y <= 5
hyp h5: 0 <= z <= 5
goal: false

problem dark_shadow
// Pugh's example: real solutions, no integer one
hyp h1: 27 <= 11*x + 13*y
hyp h2: 11*x + 13*y <= 45
hyp h3: -10 <= 7*x - 9*y
hyp h4: 7*x - 9*y <= 4
goal: false

problem dark_shadow_sat
hyp h1: 27 <= 11*x + 13*y
hyp h2: 11*x + 13*y <= 65
hyp h3: -10 <= 7*x - 9*y
hyp h4: 7*x - 9*y <= 4
goal: false

problem two_strips
// a parallelogram without integer points (the dark shadow refutes it)
hyp h1: 2 <= 3*x - 2*y
hyp h2: 3*x - 2*y <= 3
hyp h3: 5 <= 4*x + 3*y
hyp h4: 4*x + 3*y <= 6
goal: false

problem strip_without_points
hyp h1: 1 <= 5*x - 5*y
hyp h2: 5*x - 5*y <= 4
goal: false

problem two_var_parity
hyp h1: x + y == 2*k + 1
hyp h2: x - y == 2*m
goal: false

// Found by the random test: the real shadow is satisfiable, the dark
// shadow is not, and only the splinters decide.

problem splinter_point_1
hyp s1: 27 <= 8*a - 3*b <= 31
hyp s2: 28 <= 10*a - 2*b <= 31
goal: false

problem splinter_point_2
hyp s1: 15 <= 7*a + 2*b <= 19
hyp s2: 25 <= 9*a + 4*b <= 28
goal: false

problem splinter_3
hyp s1: 14 <= 10*b - 11*a <= 15
hyp s2: 15 <= 10*a + 6*b <= 18
goal: false

problem splinter_4
hyp s1: 5 <= 11*b - 5*a <= 9
hyp s2: -5 <= 7*a - 11*b <= -3
goal: false

problem splinter_5
hyp s1: 2 <= 5*a - 4*b <= 6
hyp s2: -2 <= 3*a - 5*b <= 1
hyp s3: 13 <= 8*a + 3*b <= 15
goal: false
