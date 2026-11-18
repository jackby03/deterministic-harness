package inventory

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
)

// CountFuzzSeeds counts the f.Add calls declared inside each fuzz target of a
// Go source file. These are first-class seeds: the fuzzer runs them before it
// explores, so requiring committed testdata files on top of them would be
// duplication, and requiring nothing at all is satisfaction by absence.
func CountFuzzSeeds(src []byte, filename string) (map[string]int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	out := map[string]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !strings.HasPrefix(fn.Name.Name, "Fuzz") {
			continue
		}
		count := 0
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Add" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "f" {
				count++
			}
			return true
		})
		out[fn.Name.Name] = count
	}
	return out, nil
}

// CountCorpusFiles counts committed corpus entries for one target directory.
// A missing directory is zero, not an error: the registry decides whether zero
// is acceptable, and it is not when the target declares no f.Add seeds.
func CountCorpusFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			n++
		}
	}
	return n
}

// CheckSeeds requires every declared target to have at least min seeds,
// counting f.Add declarations plus committed corpus files.
func CheckSeeds(observed map[string]int, min int) []Violation {
	var out []Violation
	if len(observed) == 0 {
		return []Violation{{Subject: "fuzz", Kind: "missing", Detail: "no fuzz targets observed"}}
	}
	for _, target := range sortedStrings(observed) {
		if observed[target] < min {
			out = append(out, Violation{
				Subject: target,
				Kind:    "missing",
				Detail:  strconv.Itoa(observed[target]) + " seeds, minimum is " + strconv.Itoa(min),
			})
		}
	}
	sortViolations(out)
	return out
}
