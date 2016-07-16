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
package outparamcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"io/ioutil"
	"sort"
	"strings"
	"sync"

	"github.com/kisielk/gotool"
	"github.com/palantir/stacktrace"
	"golang.org/x/tools/go/loader"

	"github.com/palantir/outparamcheck/exprs"
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
			return stacktrace.Propagate(err, "Failed to load configuration from parameter %v", cfgParam)
		}
		for key, val := range usrCfg {
			cfg[key] = val
		}
	}
	// add default config (values for default will override any user-supplied config for the same keys)
	for key, val := range defaultCfg {
		cfg[key] = val
	}

	prog, err := load(paths)
	if err != nil {
		return stacktrace.Propagate(err, "")
	}
	errs := run(prog, cfg)
	if len(errs) > 0 {
		reportErrors(errs)
		return fmt.Errorf("%s; the parameters listed above require the use of '&', for example f(&x) instead of f(x)",
			plural(len(errs), "error", "errors"))
	}
	return nil
}

func run(prog *loader.Program, cfg Config) []OutParamError {
	var errs []OutParamError
	var mut sync.Mutex // guards errs
	var wg sync.WaitGroup
	for _, pkgInfo := range prog.InitialPackages() {
		if pkgInfo.Pkg.Path() == "unsafe" { // not a real package
			continue
		}

		wg.Add(1)

		go func(pkgInfo *loader.PackageInfo) {
			defer wg.Done()
			v := &visitor{
				prog:   prog,
				pkg:    pkgInfo,
				lines:  map[string][]string{},
				errors: []OutParamError{},
				cfg:    cfg,
			}
			for _, astFile := range pkgInfo.Files {
				exprs.Walk(v, astFile)
			}
			mut.Lock()
			defer mut.Unlock()
			errs = append(errs, v.errors...)
		}(pkgInfo)
	}
	wg.Wait()
	return errs
}

func loadCfgFromPath(cfgPath string) (Config, error) {
	cfgBytes, err := ioutil.ReadFile(cfgPath)
	if err != nil {
		return Config{}, stacktrace.Propagate(err, "Failed to read file at %v", cfgPath)
	}
	return loadCfg(string(cfgBytes))
}

func loadCfg(cfgJson string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJson), &cfg); err != nil {
		return Config{}, stacktrace.Propagate(err, "Failed to unmarshal json", cfgJson)
	}
	return cfg, nil
}

func load(paths []string) (*loader.Program, error) {
	loadcfg := loader.Config{
		Build: &build.Default,
	}
	includeTests := true
	rest, err := loadcfg.FromArgs(gotool.ImportPaths(paths), includeTests)
	if err != nil {
		return nil, stacktrace.Propagate(err, "could not parse arguments")
	}
	if len(rest) > 0 {
		return nil, stacktrace.NewError("unhandled extra arguments: %v", rest)
	}
	prog, err := loadcfg.Load()
	return prog, stacktrace.Propagate(err, "")
}

type visitor struct {
	prog   *loader.Program
	pkg    *loader.PackageInfo
	lines  map[string][]string
	errors []OutParamError
	cfg    Config
}

func (v *visitor) Visit(expr ast.Expr) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return
	}
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

func (v *visitor) keyAndName(call *ast.CallExpr) (key string, name string, ok bool) {
	switch target := call.Fun.(type) {
	case *ast.Ident:
		// Function calls without a selector; this includes calls within the
		// same package as well as calls into dot-imported packages
		if def, ok := v.pkg.Uses[target]; ok && def.Pkg() != nil {
			return fmt.Sprintf("%v.%v", def.Pkg().Path(), target.Name), target.Name, true
		}
	case *ast.SelectorExpr:
		// Function calls into other packages
		if recv, ok := target.X.(*ast.Ident); ok {
			if pkg, ok := v.pkg.Uses[recv].(*types.PkgName); ok {
				return fmt.Sprintf("%v.%v", pkg.Imported().Path(), target.Sel.Name), target.Sel.Name, true
			}
		}
		// Method calls
		if typ, ok := v.pkg.Types[target.X]; ok {
			return fmt.Sprintf("%v.%v", typ.Type.String(), target.Sel.Name), target.Sel.Name, true
		}
	}
	return "", "", false
}

func (v *visitor) errorAt(pos token.Pos, method string, argument int) {
	position := v.prog.Fset.Position(pos)
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
		// Allow passing literal nil
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
