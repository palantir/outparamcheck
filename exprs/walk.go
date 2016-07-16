/*
Copyright (c) 2016 Palantir Technologies

Work includes Copyright (c) 2013 Kamil Kisiel

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/
package exprs

import (
	"go/ast"
)

type Visitor interface {
	Visit(expr ast.Expr)
}

func Walk(v Visitor, node ast.Node) {
	ast.Walk(&nodeVisitor{v}, node)
}

type nodeVisitor struct {
	exprVisitor Visitor
}

func (v *nodeVisitor) Visit(node ast.Node) ast.Visitor {
	// recurse on every Statement that contains one or more Expressions
	switch stmt := node.(type) {
	case *ast.LabeledStmt:
		v.recurse(stmt.Label)
	case *ast.ExprStmt:
		v.recurse(stmt.X)
	case *ast.SendStmt:
		v.recurse(stmt.Chan)
		v.recurse(stmt.Value)
	case *ast.IncDecStmt:
		v.recurse(stmt.X)
	case *ast.AssignStmt:
		v.recurseAll(stmt.Lhs)
		v.recurseAll(stmt.Rhs)
	case *ast.GoStmt:
		v.recurse(stmt.Call)
	case *ast.DeferStmt:
		v.recurse(stmt.Call)
	case *ast.ReturnStmt:
		v.recurseAll(stmt.Results)
	case *ast.BranchStmt:
		v.recurse(stmt.Label)
	case *ast.IfStmt:
		v.recurse(stmt.Cond)
	case *ast.CaseClause:
		v.recurseAll(stmt.List)
	case *ast.SwitchStmt:
		v.recurse(stmt.Tag)
	case *ast.ForStmt:
		v.recurse(stmt.Cond)
	case *ast.RangeStmt:
		v.recurse(stmt.Key)
		v.recurse(stmt.Value)
		v.recurse(stmt.X)
	case *ast.ValueSpec:
		for _, name := range stmt.Names {
			v.recurse(name)
		}
		v.recurse(stmt.Type)
		v.recurseAll(stmt.Values)
	case *ast.TypeSpec:
		v.recurse(stmt.Name)
		v.recurse(stmt.Type)
	case *ast.FuncDecl:
		v.recurseFieldList(stmt.Recv)
		v.recurse(stmt.Name)
		v.recurse(stmt.Type)
	}
	// ast.Walk will recurse on child Statements using the same visitor
	return v
}

func (v *nodeVisitor) recurseAll(exprs []ast.Expr) {
	for _, expr := range exprs {
		v.recurse(expr)
	}
}

func (v *nodeVisitor) recurse(expr ast.Expr) {
	if expr == nil {
		return
	}
	v.exprVisitor.Visit(expr)
	// recurse on every Expression that contains one or more child Expressions
	switch expr := expr.(type) {
	case *ast.Ellipsis:
		v.recurse(expr.Elt)
	case *ast.CompositeLit:
		v.recurse(expr.Type)
		v.recurseAll(expr.Elts)
	case *ast.ParenExpr:
		v.recurse(expr.X)
	case *ast.SelectorExpr:
		v.recurse(expr.X)
		v.recurse(expr.Sel)
	case *ast.IndexExpr:
		v.recurse(expr.X)
		v.recurse(expr.Index)
	case *ast.SliceExpr:
		v.recurse(expr.X)
		v.recurse(expr.Low)
		v.recurse(expr.High)
		v.recurse(expr.Max)
	case *ast.TypeAssertExpr:
		v.recurse(expr.X)
		v.recurse(expr.Type)
	case *ast.CallExpr:
		v.recurse(expr.Fun)
		v.recurseAll(expr.Args)
	case *ast.StarExpr:
		v.recurse(expr.X)
	case *ast.UnaryExpr:
		v.recurse(expr.X)
	case *ast.BinaryExpr:
		v.recurse(expr.X)
		v.recurse(expr.Y)
	case *ast.KeyValueExpr:
		v.recurse(expr.Key)
		v.recurse(expr.Value)
	case *ast.ArrayType:
		v.recurse(expr.Len)
		v.recurse(expr.Elt)
	case *ast.StructType:
		v.recurseFieldList(expr.Fields)
	case *ast.FuncType:
		v.recurseFieldList(expr.Params)
		v.recurseFieldList(expr.Results)
	case *ast.InterfaceType:
		v.recurseFieldList(expr.Methods)
	case *ast.MapType:
		v.recurse(expr.Key)
		v.recurse(expr.Value)
	case *ast.ChanType:
		v.recurse(expr.Value)
	}
}

func (v *nodeVisitor) recurseFieldList(fieldList *ast.FieldList) {
	if fieldList != nil {
		for _, f := range fieldList.List {
			for _, n := range f.Names {
				v.recurse(n)
			}
			v.recurse(f.Type)
			if f.Tag != nil {
				v.recurse(f.Tag)
			}
		}
	}
}
