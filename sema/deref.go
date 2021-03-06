package sema

import (
	"fmt"
	"github.com/rhysd/gocaml/ast"
	. "github.com/rhysd/gocaml/types"
	"github.com/rhysd/locerr"
)

func unwrapVar(variable *Var) (Type, bool) {
	if variable.Ref != nil {
		r, ok := unwrap(variable.Ref)
		if ok {
			return r, true
		}
	}

	return nil, false
}

func unwrapFun(fun *Fun) (Type, bool) {
	r, ok := unwrap(fun.Ret)
	if !ok {
		return nil, false
	}
	fun.Ret = r
	for i, param := range fun.Params {
		p, ok := unwrap(param)
		if !ok {
			return nil, false
		}
		fun.Params[i] = p
	}
	return fun, true
}

func unwrap(target Type) (Type, bool) {
	switch t := target.(type) {
	case *Fun:
		return unwrapFun(t)
	case *Tuple:
		for i, elem := range t.Elems {
			e, ok := unwrap(elem)
			if !ok {
				return nil, false
			}
			t.Elems[i] = e
		}
	case *Array:
		e, ok := unwrap(t.Elem)
		if !ok {
			return nil, false
		}
		t.Elem = e
	case *Option:
		e, ok := unwrap(t.Elem)
		if !ok {
			return nil, false
		}
		t.Elem = e
	case *Var:
		return unwrapVar(t)
	}
	return target, true
}

type typeVarDereferencer struct {
	err      *locerr.Error
	env      *Env
	inferred InferredTypes
}

func (d *typeVarDereferencer) errIn(node ast.Expr, msg string) {
	if d.err == nil {
		d.err = locerr.ErrorIn(node.Pos(), node.End(), msg)
	} else {
		d.err = d.err.NoteAt(node.Pos(), msg)
	}
}

func (d *typeVarDereferencer) derefSym(node ast.Expr, sym *ast.Symbol) {
	symType, ok := d.env.Table[sym.Name]

	if sym.IsIgnored() {
		// Parser expands `foo; bar` to `let $unused = foo in bar`. In this situation, type of the
		// variable will never be determined because it's unused.
		// So skipping it in order to avoid unknown type error for the unused variable.
		if v, ok := symType.(*Var); ok && v.Ref == nil {
			// $unused variables are never be used. So its type may not be determined. In the case,
			// it's type should be fixed to unit type.
			v.Ref = UnitType
		}
		return
	}

	if !ok {
		panic("FATAL: Cannot dereference unknown symbol: " + sym.Name)
	}

	t, ok := unwrap(symType)
	if !ok {
		d.errIn(node, fmt.Sprintf("Cannot infer type of variable '%s'. Inferred type was '%s'", sym.DisplayName, symType.String()))
		return
	}

	// Also dereference type variable in symbol
	d.env.Table[sym.Name] = t
}

func (d *typeVarDereferencer) VisitTopdown(node ast.Expr) ast.Visitor {
	switch n := node.(type) {
	case *ast.Let:
		d.derefSym(n, n.Symbol)
	case *ast.LetRec:
		// Note:
		// Need to dereference parameters at first because type of the function depends on type
		// of its parameters and parameters may be specified as '_'.
		// '_' is unused. So its type may not be determined and need to be fixed as unit type.
		for _, p := range n.Func.Params {
			d.derefSym(n, p.Ident)
		}
		d.derefSym(n, n.Func.Symbol)
	case *ast.LetTuple:
		for _, sym := range n.Symbols {
			d.derefSym(n, sym)
		}
	case *ast.Match:
		d.derefSym(n, n.SomeIdent)
	}
	return d
}

func (d *typeVarDereferencer) checkLess(op string, lhs ast.Expr) string {
	operand, ok := d.inferred[lhs]
	if !ok {
		panic("FATAL: Operand type of operator '" + op + "' not found at " + lhs.Pos().String())
	}
	// Note:
	// This type constraint may be useful for type inference. But current HM type inference algorithm cannot
	// handle a union type. In this context, the operand should be `int | float`
	switch operand.(type) {
	case *Unit, *Bool, *String, *Fun, *Tuple, *Array, *Option:
		return fmt.Sprintf("'%s' can't be compared with operator '%s'", operand.String(), op)
	default:
		return ""
	}
}

func (d *typeVarDereferencer) checkEq(op string, lhs ast.Expr) string {
	operand, ok := d.inferred[lhs]
	if !ok {
		panic("FATAL: Operand type of operator '" + op + "' not found at " + lhs.Pos().String())
	}
	// Note:
	// This type constraint may be useful for type inference. But current HM type inference algorithm cannot
	// handle a union type. In this context, the operand should be `() | bool | int | float | fun<R, TS...> | tuple<Args...>`
	if a, ok := operand.(*Array); ok {
		return fmt.Sprintf("Array type '%s' can't be compared with operator '%s'", a.String(), op)
	}
	return ""
}

func (d *typeVarDereferencer) miscCheck(node ast.Expr) {
	msg := ""
	switch n := node.(type) {
	case *ast.Less:
		msg = d.checkLess("<", n.Left)
	case *ast.LessEq:
		msg = d.checkLess("<=", n.Left)
	case *ast.Greater:
		msg = d.checkLess(">", n.Left)
	case *ast.GreaterEq:
		msg = d.checkLess(">=", n.Left)
	case *ast.Eq:
		msg = d.checkEq("=", n.Left)
	case *ast.NotEq:
		msg = d.checkEq("<>", n.Left)
	}
	if msg != "" {
		d.errIn(node, msg)
	}
}

func (d *typeVarDereferencer) VisitBottomup(node ast.Expr) {
	d.miscCheck(node)

	// Dereference all nodes' types
	t, ok := d.inferred[node]
	if !ok {
		return
	}

	unwrapped, ok := unwrap(t)
	if !ok {
		d.errIn(node, fmt.Sprintf("Cannot infer type of expression. Type annotation is needed. Inferred type was '%s'", t.String()))
		return
	}

	d.inferred[node] = unwrapped
}

func derefTypeVars(env *Env, root ast.Expr, inferred InferredTypes) error {
	v := &typeVarDereferencer{nil, env, inferred}

	// Note:
	// Don't need to dereference types of external symbols because they must not contain any
	// type variables.
	ast.Visit(v, root)

	// Note:
	// Cannot return v.err directly because `return v.err` returns typed nil (typed as *locerr.Error).
	if v.err != nil {
		return v.err
	}
	return nil
}
