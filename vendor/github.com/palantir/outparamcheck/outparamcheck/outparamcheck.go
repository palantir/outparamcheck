// Copyright 2013 Kamil Kisiel
// Modifications copyright 2016 Palantir Technologies, Inc.
// Licensed under the MIT License. See LICENSE in the project root
// for license information.

package outparamcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"io/ioutil"
	"sort"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
)

func Run(cfgParam string, paths []string) error {
	cfg := Config{}
	if cfgParam != "" {
		var usrCfg Config
		var err error
		if strings.HasPrefix(cfgParam, "@") {
			usrCfg, err = loadCfgFromPath(cfgParam[1:])
		} else {
			usrCfg, err = loadCfg(cfgParam)
		}
		if err != nil {
			return errors.Wrapf(err, "Failed to load configuration from parameter %s", cfgParam)
		}
		for key, val := range usrCfg {
			cfg[key] = val
		}
	}
	// add default config (values for default will override any user-supplied config for the same keys)
	for key, val := range defaultCfg {
		cfg[key] = val
	}

	pkgs, err := load(paths)
	if err != nil {
		return errors.WithStack(err)
	}
	errs := run(pkgs, cfg)
	if len(errs) > 0 {
		reportErrors(errs)
		return fmt.Errorf("%s; the parameters listed above require the use of '&', for example f(&x) instead of f(x)",
			plural(len(errs), "error", "errors"))
	}
	return nil
}

func run(pkgs []*packages.Package, cfg Config) []OutParamError {
	var errs []OutParamError
	var mut sync.Mutex // guards errs
	var wg sync.WaitGroup
	for _, pkg := range pkgs {
		wg.Add(1)

		go func(pkg *packages.Package) {
			defer wg.Done()
			v := &visitor{
				pkg:    pkg,
				lines:  map[string][]string{},
				errors: []OutParamError{},
				cfg:    cfg,
			}
			for _, astFile := range v.pkg.Syntax {
				ast.Walk(v, astFile)
			}
			mut.Lock()
			defer mut.Unlock()
			errs = append(errs, v.errors...)
		}(pkg)
	}
	wg.Wait()
	return errs
}

func loadCfgFromPath(cfgPath string) (Config, error) {
	cfgBytes, err := ioutil.ReadFile(cfgPath)
	if err != nil {
		return Config{}, errors.Wrapf(err, "failed to read file %s", cfgPath)
	}
	return loadCfg(string(cfgBytes))
}

func loadCfg(cfgJSON string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		return Config{}, errors.Wrapf(err, "failed to unmarshal json %s", cfgJSON)
	}
	return cfg, nil
}

func load(paths []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode:  packages.LoadAllSyntax,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, paths...)
	if err != nil {
		return nil, err
	}
	// check for errors in the initial packages
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return nil, fmt.Errorf("errors while loading package %s: %v", pkg.ID, pkg.Errors)
		}
	}
	return pkgs, nil
}

type visitor struct {
	pkg    *packages.Package
	lines  map[string][]string
	errors []OutParamError
	cfg    Config
}

func (v *visitor) Visit(node ast.Node) ast.Visitor {
	switch stmt := node.(type) {
	case *ast.AssignStmt:
		for _, expr := range stmt.Rhs {
			v.processExpression(expr)
		}
	case *ast.GoStmt:
		v.processExpression(stmt.Call)
	case *ast.DeferStmt:
		v.processExpression(stmt.Call)
	case *ast.SendStmt:
		v.processExpression(stmt.Value)
	case *ast.ReturnStmt:
		for _, expr := range stmt.Results {
			v.processExpression(expr)
		}
	case *ast.SwitchStmt:
		for _, stmt := range stmt.Body.List {
			if caseClauseStmt, ok := stmt.(*ast.CaseClause); ok {
				for _, expr := range caseClauseStmt.List {
					v.processExpression(expr)
				}
			}
		}
	case *ast.ExprStmt:
		v.processExpression(stmt.X)
	}
	return v
}

func (v *visitor) processExpression(expr ast.Expr) {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		v.processExpression(expr.X)
		v.processExpression(expr.Y)
	case *ast.KeyValueExpr:
		v.processExpression(expr.Value)
	case *ast.CompositeLit:
		for _, subExpr := range expr.Elts {
			v.processExpression(subExpr)
		}
	case *ast.CallExpr:
		call := expr
		key, method, ok := v.keyAndName(call)
		if !ok {
			return
		}
		for name, outs := range v.cfg {
			// Suffix-matching so they also apply to vendored packages
			if strings.HasSuffix(key, name) {
				for _, i := range outs {
					arg := call.Args[i]
					if !isAddr(arg) {
						v.errorAt(arg.Pos(), method, i)
					}
				}
			}
		}
	}
}

func (v *visitor) keyAndName(call *ast.CallExpr) (key string, name string, ok bool) {
	switch target := call.Fun.(type) {
	case *ast.Ident:
		// Function calls without a selector; this includes calls within the
		// same package as well as calls into dot-imported packages
		if def, ok := v.pkg.TypesInfo.Uses[target]; ok && def.Pkg() != nil {
			return fmt.Sprintf("%v.%v", def.Pkg().Path(), target.Name), target.Name, true
		}
	case *ast.SelectorExpr:
		// Function calls into other packages
		if recv, ok := target.X.(*ast.Ident); ok {
			if pkg, ok := v.pkg.TypesInfo.Uses[recv].(*types.PkgName); ok {
				return fmt.Sprintf("%v.%v", pkg.Imported().Path(), target.Sel.Name), target.Sel.Name, true
			}
		}
		// Method calls
		if typ, ok := v.pkg.TypesInfo.Types[target.X]; ok {
			return fmt.Sprintf("%v.%v", typ.Type.String(), target.Sel.Name), target.Sel.Name, true
		}
	}
	return "", "", false
}

func (v *visitor) errorAt(pos token.Pos, method string, argument int) {
	position := v.pkg.Fset.Position(pos)
	lines, ok := v.lines[position.Filename]
	if !ok {
		contents, err := ioutil.ReadFile(position.Filename)
		if err != nil {
			contents = nil
		}
		lines = strings.Split(string(contents), "\n")
		v.lines[position.Filename] = lines
	}

	var line string
	if position.Line-1 < len(lines) {
		line = strings.TrimSpace(lines[position.Line-1])
	}
	v.errors = append(v.errors, OutParamError{position, line, method, argument})
}

func isAddr(expr ast.Expr) bool {
	switch expr := expr.(type) {
	case *ast.UnaryExpr:
		// The expected usage for output parameters, which is &x
		return expr.Op == token.AND
	case *ast.StarExpr:
		// Allow *&x as an explicit way to signal that no & is intended
		child, ok := expr.X.(*ast.UnaryExpr)
		return ok && child.Op == token.AND
	case *ast.Ident:
		if expr.Obj != nil && expr.Obj.Decl != nil {
			switch child := expr.Obj.Decl.(type) {
			case *ast.AssignStmt:
				return isAddr(child.Rhs[0])
			}
		}
		// Allow passing a pointer or literal nil
		return expr.Name == "nil"
	default:
		return false
	}
}

func reportErrors(errs []OutParamError) {
	sort.Sort(byLocation(errs))
	for _, err := range errs {
		fmt.Println(err)
	}
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
}
