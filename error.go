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
	"fmt"
	"go/token"
	"strings"

	"github.com/dustin/go-humanize"
)

type OutParamError struct {
	Pos      token.Position
	Line     string
	Method   string
	Argument int
}

func (err OutParamError) Error() string {
	pos := err.Pos.String()
	// Trim prefix including /src/
	if i := strings.Index(pos, "/src/"); i != -1 {
		pos = pos[i+len("/src/"):]
	}

	line := err.Line
	comment := strings.Index(line, "//")
	if comment != -1 {
		line = line[:comment]
	}
	line = strings.TrimSpace(line)

	ord := humanize.Ordinal(err.Argument + 1)
	return fmt.Sprintf("%s\t%s  // %s argument of '%s' requires '&'", pos, line, ord, err.Method)
}

type byLocation []OutParamError

func (errs byLocation) Len() int {
	return len(errs)
}

func (errs byLocation) Swap(i, j int) {
	errs[i], errs[j] = errs[j], errs[i]
}

func (errs byLocation) Less(i, j int) bool {
	ei, ej := errs[i], errs[j]
	pi, pj := ei.Pos, ej.Pos
	if pi.Filename != pj.Filename {
		return pi.Filename < pj.Filename
	}
	if pi.Line != pj.Line {
		return pi.Line < pj.Line
	}
	if pi.Column < pj.Column {
		return pi.Column < pj.Column
	}
	return ei.Line < ej.Line
}
