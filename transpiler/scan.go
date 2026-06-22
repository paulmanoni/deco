package transpiler

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Hit is one //@<keyword> directive found on a top-level function declaration.
// It is the read-only counterpart to the wrap/codegen modes: instead of
// rewriting the function, Scan surfaces the annotation as data so a downstream
// tool can generate whatever it likes from it (e.g. nexus turns //@rest into a
// route registration). deco itself stays oblivious to what the keywords mean.
type Hit struct {
	Pkg     string         // package name the function lives in
	File    string         // absolute path of the source file
	Func    string         // annotated function name (honors a //deco:wrapper override)
	Keyword string         // directive keyword WITHOUT the leading '@', e.g. "rest"
	Args    []string       // whitespace-split tokens after the keyword
	Pos     token.Position // position of the directive line
}

// Scan walks the package tree under dir — recursively, skipping generated
// (*_gen.go), test (*_test.go), vendor, testdata, node_modules and hidden
// directories, exactly like Generate/Overlay — and returns every //@<keyword>
// directive on a top-level function's doc comment, in deterministic
// (dir, file, source-order) sequence.
//
// If keywords is non-empty, only directives whose keyword (the token after the
// leading '@') is listed are returned; otherwise every '@'-prefixed directive
// is returned. A leading '@' on the directive is required and is stripped from
// Hit.Keyword; a leading '@' on the filter keywords is tolerated
// (Scan(dir, "@rest") and Scan(dir, "rest") behave the same).
//
// Scan never modifies source and never emits wrappers — it is the front-end for
// tools that generate their own code from the annotations.
func Scan(dir string, keywords ...string) ([]Hit, error) {
	want := make(map[string]bool, len(keywords))
	for _, k := range keywords {
		want[strings.TrimPrefix(strings.TrimSpace(k), "@")] = true
	}

	dirs, err := packageDirs(dir)
	if err != nil {
		return nil, err
	}

	var hits []Hit
	for _, d := range dirs {
		names, err := sourceFiles(d)
		if err != nil {
			return nil, err
		}
		fset := token.NewFileSet()
		for _, path := range names {
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", path, err)
			}
			pkg := f.Name.Name
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Doc == nil {
					continue
				}
				hits = append(hits, scanDoc(fn, pkg, path, fset, want)...)
			}
		}
	}
	return hits, nil
}

// scanDoc extracts the //@ directives from one function's doc group.
func scanDoc(fn *ast.FuncDecl, pkg, path string, fset *token.FileSet, want map[string]bool) []Hit {
	// First resolve the public-facing name: if deco already wrapped this
	// function on a previous run, the //deco:wrapper marker carries the
	// original name and the current decl name is the generated impl.
	name := fn.Name.Name
	for _, c := range fn.Doc.List {
		content := strings.TrimSpace(strings.TrimLeft(c.Text, "/"))
		if strings.HasPrefix(content, markerKey) {
			if o := strings.TrimSpace(strings.TrimPrefix(content, markerKey)); o != "" {
				name = o
			}
		}
	}

	var hits []Hit
	for _, c := range fn.Doc.List {
		content := strings.TrimSpace(strings.TrimLeft(c.Text, "/"))
		if !strings.HasPrefix(content, "@") {
			continue
		}
		fields := strings.Fields(content[1:]) // drop the '@'
		if len(fields) == 0 {
			continue
		}
		kw := fields[0]
		if len(want) > 0 && !want[kw] {
			continue
		}
		hits = append(hits, Hit{
			Pkg:     pkg,
			File:    path,
			Func:    name,
			Keyword: kw,
			Args:    fields[1:],
			Pos:     fset.Position(c.Slash),
		})
	}
	return hits
}

// sourceFiles lists the scannable .go files in a single directory (absolute,
// sorted), applying the same generated/test exclusions analyze uses.
func sourceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") {
			continue
		}
		if strings.HasSuffix(n, "_gen.go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		abs, err := filepath.Abs(filepath.Join(dir, n))
		if err != nil {
			return nil, err
		}
		names = append(names, abs)
	}
	sort.Strings(names)
	return names, nil
}
