package types

import (
	"github.com/illumination-k/kekkai/internal/syntax"
)

// The linearity check guarantees that the `Tx` bound by every
// `transaction` block is consumed (by `commit` or `rollback`) exactly once
// on every normal exit path, and never used afterwards. Leaving the block
// through `?` is allowed with an unconsumed `Tx`: the runtime rolls it back.

// lstate maps each live linear binding to whether it has been consumed.
// A nil lstate means the current point is unreachable (diverged).
type lstate map[*Binding]bool

func (s lstate) clone() lstate {
	if s == nil {
		return nil
	}
	out := make(lstate, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

type linChecker struct {
	info  *Info
	errs  *syntax.ErrorList
	loops []lstate // states at the entry of enclosing loops
}

func checkLinearity(info *Info, fn *Func, errs *syntax.ErrorList) {
	lc := &linChecker{info: info, errs: errs}
	lc.block(fn.Decl.Body, lstate{})
}

func (lc *linChecker) errorf(pos syntax.Pos, format string, args ...any) {
	lc.errs.Add(pos, format, args...)
}

// merge joins the states of two control-flow paths.
func (lc *linChecker) merge(pos syntax.Pos, a, b lstate, what string) lstate {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	out := lstate{}
	for k, va := range a {
		vb, ok := b[k]
		if ok && va != vb {
			lc.errorf(pos, "transaction `%s` is ended in one %s but not in the other: it must be committed or rolled back on every path", k.Name, what)
		}
		out[k] = va || vb
	}
	return out
}

func (lc *linChecker) block(b *syntax.Block, s lstate) lstate {
	for _, st := range b.Stmts {
		if s == nil {
			return nil
		}
		s = lc.stmt(st, s)
	}
	if s != nil && b.Tail != nil {
		s = lc.expr(b.Tail, s)
	}
	return s
}

func (lc *linChecker) stmt(st syntax.Stmt, s lstate) lstate {
	switch st := st.(type) {
	case *syntax.LetStmt:
		return lc.expr(st.Init, s)
	case *syntax.AssignStmt:
		return lc.expr(st.Value, s)
	case *syntax.ExprStmt:
		return lc.expr(st.X, s)
	case *syntax.FieldAssignStmt:
		s = lc.expr(st.Target, s)
		return lc.expr(st.Value, s)
	case *syntax.BreakStmt, *syntax.ContinueStmt:
		if n := len(lc.loops); n > 0 {
			for k, v := range s {
				if entry, ok := lc.loops[n-1][k]; ok && entry != v {
					lc.errorf(stmtPos(st), "transaction `%s` cannot be committed or rolled back inside a loop", k.Name)
				}
			}
		}
		return nil
	case *syntax.WhileStmt:
		s = lc.expr(st.Cond, s)
		if s == nil {
			return nil
		}
		lc.loops = append(lc.loops, s.clone())
		after := lc.block(st.Body, s.clone())
		lc.loops = lc.loops[:len(lc.loops)-1]
		if after != nil {
			for k, v := range after {
				if s[k] != v {
					lc.errorf(st.Pos, "transaction `%s` cannot be committed or rolled back inside a loop", k.Name)
				}
			}
		}
		return s
	case *syntax.ForStmt:
		s = lc.expr(st.Iter, s)
		if st.End != nil {
			s = lc.expr(st.End, s)
		}
		if s == nil {
			return nil
		}
		lc.loops = append(lc.loops, s.clone())
		after := lc.block(st.Body, s.clone())
		lc.loops = lc.loops[:len(lc.loops)-1]
		if after != nil {
			for k, v := range after {
				if s[k] != v {
					lc.errorf(st.Pos, "transaction `%s` cannot be committed or rolled back inside a loop", k.Name)
				}
			}
		}
		return s
	case *syntax.ReturnStmt:
		if st.Value != nil {
			s = lc.expr(st.Value, s)
			if s == nil {
				return nil
			}
		}
		if ri := lc.info.Returns[st]; ri != nil && ri.Closure != nil {
			if consumed, live := s[ri.Closure.Tx]; live && !consumed {
				lc.errorf(st.Pos, "transaction `%s` must be committed or rolled back before `return`", ri.Closure.Tx.Name)
			}
		}
		return nil
	}
	return s
}

func (lc *linChecker) use(id *syntax.Ident, s lstate) {
	b := lc.info.Uses[id]
	if b == nil {
		return
	}
	if consumed, live := s[b]; live && consumed {
		lc.errorf(id.Pos, "use of transaction `%s` after it was committed or rolled back", b.Name)
	}
}

func (lc *linChecker) exprs(es []syntax.Expr, s lstate) lstate {
	for _, e := range es {
		if s == nil {
			return nil
		}
		s = lc.expr(e, s)
	}
	return s
}

func (lc *linChecker) expr(e syntax.Expr, s lstate) lstate {
	if s == nil {
		return nil
	}
	switch e := e.(type) {
	case *syntax.Ident:
		lc.use(e, s)
		return s
	case *syntax.UnaryExpr:
		return lc.expr(e.X, s)
	case *syntax.BinaryExpr:
		s = lc.expr(e.X, s)
		if e.Op == syntax.AmpAmp || e.Op == syntax.PipePipe {
			before := s.clone()
			after := lc.expr(e.Y, s)
			if after != nil {
				for k, v := range after {
					if before[k] != v {
						lc.errorf(e.Pos, "transaction `%s` cannot be ended in a conditionally evaluated operand", k.Name)
					}
				}
			}
			return before
		}
		return lc.expr(e.Y, s)
	case *syntax.Block:
		return lc.block(e, s)
	case *syntax.IfExpr:
		s = lc.expr(e.Cond, s)
		if s == nil {
			return nil
		}
		then := lc.block(e.Then, s.clone())
		els := s.clone()
		if e.Else != nil {
			els = lc.expr(e.Else, els)
		}
		return lc.merge(e.Pos, then, els, "branch of this `if`")
	case *syntax.MatchExpr:
		s = lc.expr(e.X, s)
		if s == nil {
			return nil
		}
		var out lstate
		first := true
		for _, arm := range e.Arms {
			as := lc.expr(arm.Body, s.clone())
			if first {
				out, first = as, false
			} else {
				out = lc.merge(arm.Pos, out, as, "arm of this `match`")
			}
		}
		return out
	case *syntax.CallExpr:
		return lc.exprs(e.Args, s)
	case *syntax.MethodCall:
		mi := lc.info.Methods[e]
		if mi != nil && mi.Kind == MethodTransaction {
			return lc.transaction(e, mi, s)
		}
		s = lc.expr(e.Recv, s)
		s = lc.exprs(e.Args, s)
		if s == nil {
			return nil
		}
		if mi != nil && mi.Builtin != nil && mi.Builtin.Consumes {
			if id, ok := e.Recv.(*syntax.Ident); ok {
				if b := lc.info.Uses[id]; b != nil {
					if consumed, live := s[b]; live && !consumed {
						s[b] = true
					}
				}
			}
		}
		return s
	case *syntax.FieldExpr:
		return lc.expr(e.X, s)
	case *syntax.StructLit:
		for _, f := range e.Fields {
			s = lc.expr(f.Value, s)
		}
		return s
	case *syntax.TryExpr:
		// The error path leaves the enclosing transaction body (or function);
		// an unconsumed Tx is rolled back automatically on that path.
		return lc.expr(e.X, s)
	}
	return s
}

func (lc *linChecker) transaction(e *syntax.MethodCall, mi *MethodInfo, s lstate) lstate {
	s = lc.expr(e.Recv, s)
	if s == nil {
		return nil
	}
	cl := e.Args[0].(*syntax.Closure)
	tx := mi.Closure.Tx
	inner := s.clone()
	inner[tx] = false
	savedLoops := lc.loops
	lc.loops = nil
	after := lc.expr(cl.Body, inner)
	lc.loops = savedLoops
	if after != nil && !after[tx] {
		pos := syntax.ExprPos(cl.Body)
		if b, ok := cl.Body.(*syntax.Block); ok {
			pos = b.End
			if b.Tail != nil {
				pos = syntax.ExprPos(b.Tail)
			}
		}
		lc.errorf(pos, "transaction `%s` is never committed or rolled back: end the block with `%s.commit()` or call `%s.rollback()`", tx.Name, tx.Name, tx.Name)
	}
	return s
}
