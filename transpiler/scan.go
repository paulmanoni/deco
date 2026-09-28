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

// Hit is one //@<keyword> directive found on a top-level function declaration
// or on a file's package doc comment. It is the read-only counterpart to the
// wrap/codegen modes: instead of rewriting anything, Scan surfaces the
// annotation as data so a downstream tool can generate whatever it likes from
// it (e.g. nexus turns //@rest into a route registration, and a package-level
// //@module into the registration group). deco itself stays oblivious to what
// the keywords mean.
type Hit struct {
	Pkg     string         // package name the directive lives in
	File    string         // absolute path of the source file
	Func    string         // annotated function name ("" for a package-level hit)
	Keyword string         // directive keyword WITHOUT the leading '@', e.g. "rest"
	Args    []string       // whitespace-split tokens after the keyword
	Pos     token.Position // position of the directive line

	// PackageLevel marks a directive found on the package clause's doc
	// comment rather than on a function — package-scoped metadata like a
	// module name or a route prefix. Func is "" for these.
	PackageLevel bool
}

// Scan walks the package tree under dir — recursively, skipping generated
// (*_gen.go), test (*_test.go), vendor, testdata, node_modules and hidden
// directories, exactly like Generate/Overlay — and returns every //@<keyword>
// directive on a top-level function's doc comment or on a package clause's
// doc comment (Hit.PackageLevel), in deterministic (dir, file, source-order)
// sequence.
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

	return scanTree(dir, want, nil)
}

// scanTree walks the tree and collects hits, filtering with want (empty =
// all). When cache is non-nil, a file whose (mtime, size) matches the cached
// entry reuses its parsed hits — the incremental path [ScanCache] provides.
func scanTree(dir string, want map[string]bool, cache *ScanCache) ([]Hit, error) {
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
		for _, path := range names {
			fileHits, err := cache.fileHits(path)
			if err != nil {
				return nil, err
			}
			hits = append(hits, filterHits(fileHits, want)...)
		}
	}
	return hits, nil
}

// scanFile parses one source file and returns EVERY //@ directive in it —
// the unfiltered form the cache stores, so one cache serves any keyword set.
func scanFile(path string) ([]Hit, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	pkg := f.Name.Name
	var hits []Hit
	if f.Doc != nil {
		hits = append(hits, scanComments(f.Doc, "", true, pkg, path, fset, nil)...)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		hits = append(hits, scanDoc(fn, pkg, path, fset, nil)...)
	}
	return hits, nil
}

// filterHits applies the keyword filter; an empty filter keeps everything.
func filterHits(hits []Hit, want map[string]bool) []Hit {
	if len(want) == 0 {
		return hits
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		if want[h.Keyword] {
			out = append(out, h)
		}
	}
	return out
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

	return scanComments(fn.Doc, name, false, pkg, path, fset, want)
}

// scanComments extracts the //@ directives from one comment group — a
// function's doc, or (packageLevel) the package clause's doc.
func scanComments(doc *ast.CommentGroup, funcName string, packageLevel bool, pkg, path string, fset *token.FileSet, want map[string]bool) []Hit {
	var hits []Hit
	for _, c := range doc.List {
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
			Pkg:          pkg,
			File:         path,
			Func:         funcName,
			Keyword:      kw,
			Args:         fields[1:],
			Pos:          fset.Position(c.Slash),
			PackageLevel: packageLevel,
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
