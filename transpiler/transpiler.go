// Package transpiler implements the comment-hosted decorator transpiler.
//
// The model, in one breath: a user annotates any function or method with
// //deco:wrap doc comments (or the //@decorate alias); we rename that
// function to an unexported
// <name>Impl and generate a brand-new function with the original name and an
// identical signature that builds the decorator chain over <name>Impl. Every
// existing caller of the original name therefore transparently flows through
// the decorators — exactly like Python's fn = a(b(fn)).
//
// Strategy A (rename-and-wrap), end to end:
//
//  1. Parse every non-generated .go file in the directory with comments intact.
//  2. For each func declaration, read its doc group for //deco:wrap (or
//     alias) lines.
//  3. From the full *ast.FuncType, faithfully reproduce the signature (named,
//     unnamed, grouped and variadic params; zero/one/many results).
//  4. Surgically rename the original function to <name>Impl in place (a precise
//     text edit so the rest of the file — comments, formatting — is untouched)
//     and stamp a //deco:wrapper marker so re-runs are idempotent.
//  5. Emit a fully type-safe wrapper into <file>_gen.go, formatted with
//     go/format and carrying the standard "DO NOT EDIT" header.
//
// Methods are supported too: the method is renamed in place (receiver kept)
// and the chain is built over the METHOD EXPRESSION — e.g.
// logged((*Service).doImpl) — a plain func value whose first parameter is the
// receiver, so any function decorator wraps methods unchanged; the generated
// wrapper is a same-signature method that forwards the receiver. Generic
// receivers are the one exclusion (a type parameter prevents the
// package-level method expression) and are rejected with a clear file:line
// error.
//
// # Using it as a library
//
// The same engine the `deco` CLI uses is exported here, so you can run the
// transpiler from your own Go programs or tooling — no CLI required:
//
//	import "github.com/paulmanoni/deco/transpiler"
//
//	// Write <file>_gen.go wrappers across the package tree under dir:
//	if err := transpiler.Generate("./mypkg"); err != nil { ... }
//
//	// Or get the generated content in memory, without touching disk:
//	outs, err := transpiler.Transform("./mypkg")
//	for _, o := range outs { fmt.Println(o.Path, len(o.Content)) }
//
//	// Or build an overlay for `go build -overlay` (source left untouched):
//	overlay, cleanup, err := transpiler.Overlay("./mypkg")
//	defer cleanup()
//
// A common pattern is a go:generate directive that needs no installed binary:
//
//	//go:generate go run github.com/paulmanoni/deco generate .
package transpiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

// marker is the comment we stamp above a renamed function so that subsequent
// runs can recover the original (wrapper) name and know the rename is already
// done. It keeps generation idempotent. The "//word:" shape is a gofmt
// directive form, so gofmt leaves it untouched (no space inserted).
const marker = "//deco:wrapper "

// markerKey / wrapDirectiveKey / defaultAnnotation are the slash-stripped forms
// used when scanning doc comments, so we match regardless of any space gofmt
// inserts after "//".
const (
	markerKey = "deco:wrapper"
	// wrapDirectiveKey is the primary decorator syntax, in Go's own
	// //tool:directive form (like //go:embed): //deco:wrap name. It is always
	// recognised, regardless of WithAnnotation.
	wrapDirectiveKey = "deco:wrap"
	// defaultAnnotation is the alias keyword that also introduces a decorator
	// when none is configured: //@decorate name. Override it with WithAnnotation.
	defaultAnnotation = "@decorate"
	// importDirectiveKey introduces an import to inject into generated files so
	// that qualified decorators (pkg.Name) resolve, e.g.
	//   //deco:import "github.com/you/yourdecorators"
	//   //deco:import alias "github.com/you/yourdecorators"
	importDirectiveKey = "deco:import"
)

// config holds tunable transpiler settings, populated from Options.
type config struct {
	annotation string // the slash-stripped alias keyword that introduces a decorator
}

// An Option customises how the transpiler reads annotations.
type Option func(*config)

// WithAnnotation sets the alias doc-comment keyword that introduces a
// decorator. The default is "@decorate" (i.e. //@decorate name). Provide the
// full keyword including any leading "@" you want: WithAnnotation("@wrap")
// matches //@wrap name, while WithAnnotation("decorate") matches
// //decorate name. An empty keyword is ignored (the default is kept). The
// directive form //deco:wrap name is always recognised in addition to the
// alias.
func WithAnnotation(keyword string) Option {
	return func(c *config) {
		if keyword != "" {
			c.annotation = keyword
		}
	}
}

func newConfig(opts []Option) config {
	c := config{annotation: defaultAnnotation}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// generatedHeader marks files we own; we never scan these for annotations and
// always overwrite them wholesale.
const generatedHeader = "// Code generated by deco; DO NOT EDIT."

// decorator is a single parsed //deco:wrap (or alias) annotation.
type decorator struct {
	name      string   // the (possibly qualified) decorator identifier, e.g. "logged" or "pkg.Logged"
	args      string   // rendered leading arguments, e.g. `"slow"`; empty when none
	argN      int      // number of leading arguments supplied
	line      int      // source line of the annotation, for diagnostics
	selectors []string // package selectors referenced (head + args), e.g. ["decorators"]
	factory   bool     // a middleware factory (returns decorators.Middleware) — fusable
}

// funcInfo is what the package-wide function scan records per plain function:
// enough to resolve, arity-check and classify a bare decorator reference.
type funcInfo struct {
	params  int  // value-parameter count
	factory bool // single result of type decorators.Middleware
}

// decoratorsImportPath is deco's middleware toolkit; a fused chain in a
// generated file imports it for Chain/Run and the Middleware type.
const decoratorsImportPath = "github.com/paulmanoni/deco/decorators"

// isMiddlewareFactory reports whether a function's signature is the middleware
// FACTORY shape: exactly one result, of deco's decorators.Middleware type,
// under whatever selector the declaring file imports that package as. Only a
// selector that provably resolves to decoratorsImportPath counts — a
// same-named local type must not be fused over.
func isMiddlewareFactory(fn *ast.FuncType, imports map[string]string) bool {
	if fn.Results == nil || len(fn.Results.List) != 1 || len(fn.Results.List[0].Names) > 1 {
		return false
	}
	sel, ok := fn.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Middleware" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return importPathOfLine(imports[id.Name]) == decoratorsImportPath
}

// mwImport picks the selector and import line the generated file uses for
// deco's decorators package: the source file's own selector when it already
// imports that package, the conventional "decorators" otherwise, or the
// "decomw" alias when the plain selector is taken by a different package.
func mwImport(fileImports map[string]string) (sel, line string) {
	sels := slices.Sorted(maps.Keys(fileImports))
	for _, s := range sels {
		if importPathOfLine(fileImports[s]) == decoratorsImportPath {
			return s, fileImports[s]
		}
	}
	if _, taken := fileImports["decorators"]; taken {
		return "decomw", "decomw " + strconv.Quote(decoratorsImportPath)
	}
	return "decorators", strconv.Quote(decoratorsImportPath)
}

// importPathOfLine extracts the quoted import path from an import line as
// stored in the selector maps (`"path"` or `alias "path"`).
func importPathOfLine(line string) string {
	if line == "" {
		return ""
	}
	if i := strings.IndexByte(line, '"'); i >= 0 {
		if p, err := strconv.Unquote(line[i:]); err == nil {
			return p
		}
	}
	return ""
}

// job is one function or method that needs a generated wrapper.
type job struct {
	wrapperName string        // the original, public-facing name (e.g. Add)
	implName    string        // the renamed implementation (e.g. addImpl)
	decorators  []decorator   // in source (top-to-bottom) order
	fn          *ast.FuncType // the full signature
	srcFile     string        // absolute path of the source file
	decl        *ast.FuncDecl // the declaration (used for rename positions)
	needsRename bool          // false when a marker shows the rename already happened

	// Method-only fields (empty for plain functions). The chain is built over
	// the METHOD EXPRESSION recvExpr.implName — a plain func value whose first
	// parameter is the receiver — so any function decorator works unchanged.
	recvSig  string // rendered receiver, e.g. "s *Service"
	recvName string // the receiver identifier the wrapper forwards, e.g. "s"
	recvType string // the receiver's base type name, e.g. "Service" (for the chain var)
	recvExpr string // method-expression qualifier, e.g. "(*Service)" or "Service"
}

// isMethod reports whether the job wraps a method rather than a plain function.
func (j job) isMethod() bool { return j.recvSig != "" }

// allFactories reports whether every decorator on the job is a middleware
// factory — the case where the generated wrapper can skip reflection entirely.
func (j job) allFactories() bool {
	for _, d := range j.decorators {
		if !d.factory {
			return false
		}
	}
	return len(j.decorators) > 0
}

// anyFactory reports whether any job in the file uses a middleware factory,
// which is what obliges the generated file to import deco's decorators
// package (for Chain/Run and the Middleware type).
func anyFactory(jobs []job) bool {
	for _, j := range jobs {
		for _, d := range j.decorators {
			if d.factory {
				return true
			}
		}
	}
	return false
}

// Output is one file's worth of generated content, keyed by the absolute path
// it represents inside the package: either a transformed (renamed) original or
// a generated <file>_gen.go wrapper. Returned by [Transform].
type Output struct {
	Path    string // absolute path this content stands in for
	Content []byte
}

// SourceMap relates positions in deco's transpiled output back to the user's
// original source, so a tool reporting a diagnostic against the overlay can
// translate the line number. It is returned by [OverlayWithSourceMap].
//
// Transformed originals only differ from the source by inserted //deco:wrapper
// marker lines, so the mapping is a simple line shift. Generated wrappers
// (*_gen.go) have no source equivalent at all.
type SourceMap struct {
	// inserted maps a transformed original's absolute LOGICAL path to the
	// sorted, 1-based line numbers that the transpiler inserted into it.
	inserted map[string][]int
	// generated is the set of absolute LOGICAL paths that are fully generated.
	generated map[string]bool
	// shadowToLogical maps each overlay shadow file's absolute path (what the go
	// toolchain actually reports in diagnostics) to its logical source path.
	shadowToLogical map[string]string
}

func newSourceMap() *SourceMap {
	return &SourceMap{
		inserted:        map[string][]int{},
		generated:       map[string]bool{},
		shadowToLogical: map[string]string{},
	}
}

// NewSourceMap builds a SourceMap from explicit data — primarily for tests and
// tools that post-process diagnostics. inserted maps a transformed file's
// absolute logical path to the 1-based line numbers inserted into it; generated
// lists the fully generated (*_gen.go) logical paths; shadows maps overlay
// shadow-file paths to their logical paths.
func NewSourceMap(inserted map[string][]int, generated []string, shadows map[string]string) *SourceMap {
	m := newSourceMap()
	maps.Copy(m.inserted, inserted)
	maps.Copy(m.shadowToLogical, shadows)
	for _, p := range generated {
		m.generated[p] = true
	}
	return m
}

// Remap translates a diagnostic position (the path and 1-based line as reported
// by the go toolchain against the overlay) back to the user's source. path may
// be an overlay shadow file or a logical path; absolute or relative to the cwd.
//
//   - known is false when the position isn't part of deco's output (an
//     untouched file): leave it alone.
//   - generated is true for a generated wrapper (*_gen.go): srcPath is the
//     logical wrapper path and srcLine equals the input line (no source line
//     exists), but the caller should still prefer srcPath over the shadow path.
//   - otherwise srcPath/srcLine give the original source position.
func (m *SourceMap) Remap(path string, line int) (srcPath string, srcLine int, generated, known bool) {
	if m == nil {
		return "", 0, false, false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if a, err := filepath.Abs(abs); err == nil {
			abs = a
		}
	}
	logical := abs
	if l, ok := m.shadowToLogical[abs]; ok {
		logical = l
	}
	if m.generated[logical] {
		return logical, line, true, true
	}
	ins, ok := m.inserted[logical]
	if !ok {
		// A shadow we don't have line data for: still rewrite the path. An
		// untouched file (logical == abs, no mapping): unknown, leave it.
		return logical, line, false, logical != abs
	}
	shift := 0
	for _, t := range ins {
		if t <= line {
			shift++
		} else {
			break
		}
	}
	return logical, line - shift, false, true
}

// Transform analyses the whole package tree under dir and returns the generated
// file contents in memory — the transformed (renamed) originals and the
// <file>_gen.go wrappers — without writing anything to disk. Use it when you
// want to inspect or post-process the output yourself; use [Generate] to write
// the files, or [Overlay] to feed them to `go build -overlay`.
func Transform(dir string, opts ...Option) ([]Output, error) {
	outputs, _, err := transformTree(dir, newConfig(opts))
	return outputs, err
}

// analysis is the parsed, validated view of a package directory.
type analysis struct {
	fset       *token.FileSet
	srcNames   []string             // sorted, absolute source paths
	files      map[string]*ast.File // by absolute path
	jobsByFile map[string][]job     // by absolute path
	resolver   importResolver       // resolves package selectors to import lines

	// fileImports maps each source path to that file's own imports
	// (selector -> import line), used to re-import types referenced by a
	// reproduced wrapper signature (e.g. http.ResponseWriter).
	fileImports map[string]map[string]string
}

// importResolver maps a package selector (the identifier used in a qualified
// decorator like `routing` in routing.Route) to the import line the generated
// file needs. It tries, in order: explicit //deco:import directives, then
// auto-resolution against the packages of the enclosing module (matched by
// package name via `go list`). A file's own imports are consulted separately by
// the caller, since they are per-file rather than package-wide.
type importResolver struct {
	directives map[string]string              // selector -> import line, from //deco:import
	modPkgs    map[string][]string            // package name -> import path(s) in the module
	dirByPath  map[string]string              // import path -> source dir (module packages)
	root       string                         // module root, cwd for external `go list` lookups
	pkgFuncs   map[string]map[string]funcInfo // per-import-path signature cache
}

// resolve returns the import line for sel, preferring (1) the source file's own
// imports, (2) //deco:import directives, (3) a unique module package of that
// name. ambiguous is true when several module packages share the name (the user
// must disambiguate with //deco:import).
func (r importResolver) resolve(sel string, fileImports map[string]string) (line string, ok, ambiguous bool) {
	if l, ok := fileImports[sel]; ok {
		return l, true, false
	}
	if l, ok := r.directives[sel]; ok {
		return l, true, false
	}
	switch paths := r.modPkgs[sel]; len(paths) {
	case 0:
		return "", false, false
	case 1:
		return strconv.Quote(paths[0]), true, false
	default:
		return "", false, true
	}
}

// funcsOf returns the plain top-level function signatures of the package at
// importPath, so qualified decorators (pkg.Name) get the same arity checking
// and factory classification as same-package ones. The package's source is
// found via the module index, or `go list` for an external dependency (which
// resolves into the module cache). Best-effort and cached per path: an
// unreadable package yields an empty map and the caller skips checking, so a
// library deco cannot see never blocks the build.
func (r importResolver) funcsOf(importPath string) map[string]funcInfo {
	if m, ok := r.pkgFuncs[importPath]; ok {
		return m
	}
	m := map[string]funcInfo{}
	r.pkgFuncs[importPath] = m // cache up front; failures stay empty (no retry)

	pkgDir, ok := r.dirByPath[importPath]
	if !ok && r.root != "" {
		cmd := exec.Command("go", "list", "-e", "-find", "-f", "{{.Dir}}", importPath)
		cmd.Dir = r.root
		if out, err := cmd.Output(); err == nil {
			pkgDir = strings.TrimSpace(string(out))
		}
	}
	if pkgDir == "" {
		return m
	}
	names, err := sourceFiles(pkgDir)
	if err != nil {
		return m
	}
	fset := token.NewFileSet()
	for _, path := range names {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue // best-effort: skip files that don't parse
		}
		imports := importsOf(f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			info := funcInfo{
				params:  paramCount(fn.Type),
				factory: isMiddlewareFactory(fn.Type, imports),
			}
			// Same name declared twice (build-tagged variants): shapes that
			// disagree make the reference uncheckable — mark it so.
			if prev, seen := m[fn.Name.Name]; seen && prev != info {
				info = funcInfo{params: -1}
			}
			m[fn.Name.Name] = info
		}
	}
	return m
}

// analyze performs steps 1–4: parse every non-generated .go file, build a
// package-wide view of declared functions, scan annotations, validate decorator
// references, and assemble the per-file job lists. It does not touch disk.
func analyze(dir string, cfg config) (*analysis, error) {
	fset := token.NewFileSet()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	// Deterministic ordering everywhere: sort absolute paths up front.
	var srcNames []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_gen.go") || strings.HasSuffix(name, "_test.go") {
			continue // never scan our own output or test files
		}
		abs, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		srcNames = append(srcNames, abs)
	}
	sort.Strings(srcNames)

	files := make(map[string]*ast.File, len(srcNames))
	for _, path := range srcNames {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		files[path] = f
	}

	// Each file's own imports, so a wrapper can re-import the packages its
	// signature types come from (and so factory detection below can resolve
	// the decorators-package selector per file).
	fileImports := make(map[string]map[string]string, len(srcNames))
	for _, path := range srcNames {
		fileImports[path] = importsOf(files[path])
	}

	// funcs maps every plain top-level function name in the package to its
	// value-parameter count and shape, so we can resolve, arity-check and
	// classify bare decorators. A function whose single result is deco's
	// decorators.Middleware (under whatever selector that file imports it) is
	// a middleware FACTORY: stacks of factories fuse into one wrapper layer.
	funcs := map[string]funcInfo{}
	for _, path := range srcNames {
		for _, decl := range files[path].Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			funcs[fn.Name.Name] = funcInfo{
				params:  paramCount(fn.Type),
				factory: isMiddlewareFactory(fn.Type, fileImports[path]),
			}
		}
	}

	// Collect //deco:import directives so qualified decorators (pkg.Name) can be
	// resolved by injecting the right import into the generated files.
	importDirectives, err := collectImportDirectives(srcNames, files, fset)
	if err != nil {
		return nil, err
	}

	// Auto-resolution: enumerate the module's packages by name (via `go list`)
	// so qualified decorators in the same module need no //deco:import directive.
	idx := moduleIndexFor(dir)
	resolver := importResolver{
		directives: importDirectives,
		modPkgs:    idx.byName,
		dirByPath:  idx.dirByPath,
		root:       moduleRoot(dir),
		pkgFuncs:   map[string]map[string]funcInfo{},
	}

	jobsByFile := map[string][]job{}
	for _, path := range srcNames {
		f := files[path]
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Doc == nil {
				continue
			}
			decs, wrapperOverride := parseAnnotations(fn.Doc, fset, cfg.annotation)
			if len(decs) == 0 {
				continue
			}
			pos := fset.Position(fn.Pos())

			for i := range decs {
				if err := classifyDecorator(&decs[i], funcs, fileImports[path], resolver, pos.Filename); err != nil {
					return nil, err
				}
			}

			j := job{decorators: decs, fn: fn.Type, srcFile: path, decl: fn}
			if fn.Recv != nil {
				if err := fillReceiver(&j, fn, fset); err != nil {
					return nil, err
				}
			}
			if wrapperOverride != "" {
				// Already renamed on a previous (on-disk) run; the marker carries
				// the public name and the current decl name is the impl.
				j.wrapperName = wrapperOverride
				j.implName = fn.Name.Name
				j.needsRename = false
			} else {
				j.wrapperName = fn.Name.Name
				j.implName = implName(fn.Name.Name)
				j.needsRename = true
			}
			jobsByFile[path] = append(jobsByFile[path], j)
		}
	}
	return &analysis{
		fset:        fset,
		srcNames:    srcNames,
		files:       files,
		jobsByFile:  jobsByFile,
		resolver:    resolver,
		fileImports: fileImports,
	}, nil
}

// transform produces, in memory, every file needed to realise the decorators:
// transformed (renamed) originals plus the generated wrappers. Nothing is
// written; callers decide whether to materialise the outputs as real files
// (Generate) or feed them to the build untouched-on-disk (Overlay).
func transform(dir string, cfg config) ([]Output, *SourceMap, error) {
	a, err := analyze(dir, cfg)
	if err != nil {
		return nil, nil, err
	}
	var outputs []Output
	sm := newSourceMap()
	for _, path := range a.srcNames {
		jobs := a.jobsByFile[path]
		if len(jobs) == 0 {
			continue
		}
		// Transformed original — only when something actually needs renaming.
		renamed, inserted, changed, err := renameBytes(path, jobs, a.fset)
		if err != nil {
			return nil, nil, err
		}
		if changed {
			outputs = append(outputs, Output{Path: path, Content: renamed})
			sm.inserted[path] = inserted
		}
		// Generated wrapper, with any imports its decorators and reproduced
		// signatures need. Fused (factory) chains additionally need deco's
		// decorators package.
		imports := importsForFile(jobs, a.fileImports[path], a.resolver)
		mwSel := ""
		if anyFactory(jobs) {
			sel, line := mwImport(a.fileImports[path])
			mwSel = sel
			if !slices.Contains(imports, line) {
				imports = append(imports, line)
				sort.Strings(imports)
			}
		}
		gen, err := genBytes(path, a.files[path].Name.Name, jobs, imports, mwSel, a.fset)
		if err != nil {
			return nil, nil, err
		}
		base := strings.TrimSuffix(filepath.Base(path), ".go")
		genPath := filepath.Join(filepath.Dir(path), base+"_gen.go")
		outputs = append(outputs, Output{Path: genPath, Content: gen})
		sm.generated[genPath] = true
	}
	return outputs, sm, nil
}

// packageDirs returns every directory at or under root that holds Go source we
// should scan, sorted for deterministic processing. It is what lets deco handle
// a multi-package tree (e.g. a router with handlers/ and middleware/ folders),
// matching the `./...` reach of `go build`. vendor, testdata, node_modules and
// hidden directories are skipped.
func packageDirs(root string) ([]string, error) {
	set := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root {
				switch name := d.Name(); {
				case strings.HasPrefix(name, "."), name == "vendor", name == "testdata", name == "node_modules":
					return filepath.SkipDir
				}
			}
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".go") &&
			!strings.HasSuffix(name, "_gen.go") && !strings.HasSuffix(name, "_test.go") {
			set[filepath.Dir(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(set))
	for d := range set {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs, nil
}

// transformTree runs transform over every package directory at or under root
// and concatenates the results, so decorators are realised across the whole
// multi-folder project, not just the top directory.
func transformTree(root string, cfg config) ([]Output, *SourceMap, error) {
	dirs, err := packageDirs(root)
	if err != nil {
		return nil, nil, err
	}
	var all []Output
	merged := newSourceMap()
	for _, d := range dirs {
		outputs, sm, err := transform(d, cfg)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, outputs...)
		maps.Copy(merged.inserted, sm.inserted)
		maps.Copy(merged.generated, sm.generated)
	}
	return all, merged, nil
}

// Generate runs the transpiler and MATERIALISES the results on disk: it renames
// originals in place (stamping the idempotency marker) and writes the
// <file>_gen.go wrappers next to them. It processes the whole package tree
// under dir.
func Generate(dir string, opts ...Option) error {
	outputs, _, err := transformTree(dir, newConfig(opts))
	if err != nil {
		return err
	}
	for _, o := range outputs {
		if err := os.WriteFile(o.Path, o.Content, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", o.Path, err)
		}
	}
	return nil
}

// Overlay runs the transpiler but keeps the generated code OUT of the source
// tree. It writes the transformed originals and the wrappers to a temp
// directory and returns the path to a `go build -overlay` JSON file mapping each
// real package path to its shadow content. The user's .go files are never
// modified and no _gen.go ever lands in the package directory — the decoration
// exists only for the duration of the build.
//
// cleanup removes the temp directory; callers should defer it. It processes the
// whole package tree under dir, so every package's decorators are injected.
func Overlay(dir string, opts ...Option) (overlayPath string, cleanup func(), err error) {
	overlayPath, _, cleanup, err = OverlayWithSourceMap(dir, opts...)
	return overlayPath, cleanup, err
}

// OverlayWithSourceMap is like [Overlay] but also returns a [SourceMap] that
// translates positions in the transpiled overlay back to the user's original
// source — useful for rewriting compiler/vet diagnostics that would otherwise
// point at generated lines.
func OverlayWithSourceMap(dir string, opts ...Option) (overlayPath string, sourceMap *SourceMap, cleanup func(), err error) {
	outputs, sm, err := transformTree(dir, newConfig(opts))
	if err != nil {
		return "", nil, nil, err
	}
	tmp, err := os.MkdirTemp("", "deco-overlay-")
	if err != nil {
		return "", nil, nil, err
	}
	cleanup = func() { os.RemoveAll(tmp) }

	// The overlay's Replace map points each real package path at a shadow file.
	// A key need not exist on disk, which is exactly how we *inject* the
	// <file>_gen.go wrappers as new members of the package.
	replace := make(map[string]string, len(outputs))
	for i, o := range outputs {
		shadow := filepath.Join(tmp, fmt.Sprintf("%d_%s", i, filepath.Base(o.Path)))
		if err := os.WriteFile(shadow, o.Content, 0o644); err != nil {
			cleanup()
			return "", nil, nil, err
		}
		replace[o.Path] = shadow
		sm.shadowToLogical[shadow] = o.Path // so diagnostics on the shadow map back
	}

	overlayPath = filepath.Join(tmp, "overlay.json")
	doc := struct{ Replace map[string]string }{Replace: replace}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		cleanup()
		return "", nil, nil, err
	}
	if err := os.WriteFile(overlayPath, data, 0o644); err != nil {
		cleanup()
		return "", nil, nil, err
	}
	return overlayPath, sm, cleanup, nil
}

// parseAnnotations scans a doc group for decorator lines — the //deco:wrap
// directive and the configured alias keyword (default //@decorate) — plus the
// //deco:wrapper marker. It returns the decorators in source order and the
// wrapper-name override carried by the marker (empty if absent).
func parseAnnotations(doc *ast.CommentGroup, fset *token.FileSet, annotation string) ([]decorator, string) {
	var decs []decorator
	var wrapperOverride string
	for _, c := range doc.List {
		// Normalise away the leading slashes and any space gofmt may have
		// inserted, so "//deco:wrap x" and "// deco:wrap x" both match.
		content := strings.TrimSpace(strings.TrimLeft(c.Text, "/"))
		if o, ok := cutKeyword(content, markerKey); ok {
			wrapperOverride = o
			continue
		}
		spec, ok := cutKeyword(content, wrapDirectiveKey)
		if !ok {
			spec, ok = cutKeyword(content, annotation)
		}
		if !ok || spec == "" {
			continue
		}
		name, args, argN, selectors := splitDecoratorSpec(spec)
		decs = append(decs, decorator{
			name:      name,
			args:      args,
			argN:      argN,
			line:      fset.Position(c.Slash).Line,
			selectors: selectors,
		})
	}
	return decs, wrapperOverride
}

// cutKeyword matches content against a leading keyword at a word boundary: the
// keyword must be followed by whitespace or the end of the line, so
// "deco:wrap" never matches a "deco:wrapper" line and "@decorate" never
// matches "@decorated". It returns the trimmed remainder.
func cutKeyword(content, keyword string) (spec string, ok bool) {
	rest, found := strings.CutPrefix(content, keyword)
	if !found || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// splitDecoratorSpec parses a decorator spec like `timing("slow")` or
// `pkg.Logged` into (name, renderedArgs, argCount, packageSelectors). It parses
// the spec as a Go expression so that qualified names (pkg.Foo), multiple
// arguments, and nested calls are all handled robustly. If parsing fails, it
// falls back to treating the whole spec as a bare name.
func splitDecoratorSpec(spec string) (name, args string, argN int, selectors []string) {
	expr, err := parser.ParseExpr(spec)
	if err != nil {
		return spec, "", 0, nil
	}
	selectors = exprSelectors(expr)
	switch e := expr.(type) {
	case *ast.CallExpr:
		var parts []string
		for _, a := range e.Args {
			parts = append(parts, exprString(a))
		}
		return exprString(e.Fun), strings.Join(parts, ", "), len(e.Args), selectors
	default:
		return exprString(expr), "", 0, selectors
	}
}

// exprSelectors returns the distinct leading package selectors used anywhere in
// expr, e.g. `pkg.Logged(other.Arg)` -> ["other", "pkg"] (sorted).
func exprSelectors(expr ast.Expr) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && !seen[id.Name] {
				seen[id.Name] = true
				out = append(out, id.Name)
			}
		}
		return true
	})
	sort.Strings(out)
	return out
}

// isQualified reports whether a decorator name is package-qualified (pkg.Name).
func isQualified(name string) bool { return strings.Contains(name, ".") }

// headSelector returns the package selector of a qualified decorator name, i.e.
// the identifier before the first dot ("pkg" for "pkg.Logged").
func headSelector(name string) string {
	if sel, _, found := strings.Cut(name, "."); found {
		return sel
	}
	return ""
}

// classifyDecorator checks that a decorator reference is usable and records
// its shape. A qualified (pkg.Name) decorator must resolve to an importable
// package; its arity and shape are not checked, since its declaration lives
// outside the scanned package (it is always treated as wrap-style). A bare
// name must resolve to a function in the package with one of two shapes:
//
//   - wrap-style: argN+1 parameters (leading args + the wrapped fn), returns
//     the same function type — applied as name(args, inner);
//   - middleware factory: exactly argN parameters and a single
//     decorators.Middleware result — fused into one wrapper layer (or a
//     reflection-free typed wrapper when the whole stack is factories).
func classifyDecorator(d *decorator, funcs map[string]funcInfo, fileImports map[string]string, resolver importResolver, file string) error {
	if isQualified(d.name) {
		sel := headSelector(d.name)
		line, ok, ambiguous := resolver.resolve(sel, fileImports)
		if ok {
			return classifyQualified(d, importPathOfLine(line), resolver, file)
		}
		if ambiguous {
			return fmt.Errorf("%s:%d: package %q for decorator %q is ambiguous (several module packages share that name); "+
				"add `//deco:import \"the/exact/path\"` to disambiguate",
				file, d.line, sel, d.name)
		}
		return fmt.Errorf("%s:%d: cannot locate package %q for decorator %q; "+
			"add `//deco:import \"path/to/%s\"` (or `//deco:import %s \"path/...\"`) to a file in this package",
			file, d.line, sel, d.name, sel, sel)
	}
	info, ok := funcs[d.name]
	if !ok {
		return fmt.Errorf("%s:%d: decorator %q not found in package", file, d.line, d.name)
	}
	return classifyShape(d, info, file)
}

// classifyQualified classifies a pkg.Name decorator by reading its defining
// package's source. When the function can't be found there — the package is
// outside the build, the decorator is a package-level var, or its declarations
// disagree across build tags — the reference is left as wrap-style with no
// check, preserving the permissive behaviour libraries relied on.
func classifyQualified(d *decorator, importPath string, resolver importResolver, file string) error {
	if importPath == "" {
		return nil
	}
	funcName := d.name[strings.IndexByte(d.name, '.')+1:]
	info, ok := resolver.funcsOf(importPath)[funcName]
	if !ok || info.params < 0 {
		return nil
	}
	return classifyShape(d, info, file)
}

// classifyShape applies the two accepted decorator shapes to a resolved
// signature: a middleware factory (exactly the leading args, fusable) or
// wrap-style (leading args + the wrapped fn).
func classifyShape(d *decorator, info funcInfo, file string) error {
	if info.factory && info.params == d.argN {
		d.factory = true
		return nil
	}
	// Wrap-style takes its leading args plus exactly one wrapped function.
	want := d.argN + 1
	if info.params != want {
		return fmt.Errorf("%s:%d: decorator %q has wrong arity: it declares %d parameter(s) "+
			"but the annotation supplies %d leading arg(s) (expected %d total: args + the wrapped fn, "+
			"or exactly %d for a middleware factory returning decorators.Middleware)",
			file, d.line, d.name, info.params, d.argN, want, d.argN)
	}
	return nil
}

// collectImportDirectives scans every comment in the package for
// //deco:import directives and returns a map from package selector to the
// import line to emit. Two forms are accepted:
//
//	//deco:import "github.com/you/pkg"          // selector = "pkg" (last path element)
//	//deco:import alias "github.com/you/pkg"    // selector = "alias"
func collectImportDirectives(srcNames []string, files map[string]*ast.File, fset *token.FileSet) (map[string]string, error) {
	out := map[string]string{}
	// Iterate files in sorted order for deterministic error reporting.
	for _, path := range srcNames {
		f := files[path]
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				content := strings.TrimSpace(strings.TrimLeft(c.Text, "/"))
				if !strings.HasPrefix(content, importDirectiveKey) {
					continue
				}
				spec := strings.TrimSpace(strings.TrimPrefix(content, importDirectiveKey))
				sel, line, err := parseImportDirective(spec)
				if err != nil {
					p := fset.Position(c.Slash)
					return nil, fmt.Errorf("%s:%d: bad //deco:import directive: %w", p.Filename, p.Line, err)
				}
				out[sel] = line
			}
		}
	}
	return out, nil
}

// parseImportDirective parses the body of a //deco:import directive into the
// package selector it introduces and the canonical import line to emit.
func parseImportDirective(spec string) (selector, importLine string, err error) {
	fields := strings.Fields(spec)
	switch len(fields) {
	case 1: // "path"
		path, uerr := strconv.Unquote(fields[0])
		if uerr != nil {
			return "", "", fmt.Errorf("expected a quoted import path, got %q", fields[0])
		}
		return lastPathElement(path), strconv.Quote(path), nil
	case 2: // alias "path"
		alias := fields[0]
		path, uerr := strconv.Unquote(fields[1])
		if uerr != nil {
			return "", "", fmt.Errorf("expected a quoted import path, got %q", fields[1])
		}
		return alias, alias + " " + strconv.Quote(path), nil
	default:
		return "", "", fmt.Errorf("expected `\"path\"` or `alias \"path\"`, got %q", spec)
	}
}

// lastPathElement returns the conventional package selector for an import path
// (its final element), e.g. "github.com/you/decorators" -> "decorators".
func lastPathElement(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// importsOf returns a file's imports as a selector -> import-line map. Blank and
// dot imports are skipped (they introduce no usable selector).
func importsOf(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		sel := lastPathElement(path)
		line := strconv.Quote(path)
		if spec.Name != nil {
			name := spec.Name.Name
			if name == "_" || name == "." {
				continue
			}
			sel = name
			line = name + " " + strconv.Quote(path)
		}
		out[sel] = line
	}
	return out
}

// signatureSelectors returns the distinct package selectors referenced by a
// function's parameter and result types (e.g. "http" for http.ResponseWriter),
// so the generated wrapper can re-import them.
func signatureSelectors(fn *ast.FuncType) []string {
	seen := map[string]bool{}
	var out []string
	collect := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			ast.Inspect(f.Type, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && !seen[id.Name] {
						seen[id.Name] = true
						out = append(out, id.Name)
					}
				}
				return true
			})
		}
	}
	collect(fn.Params)
	collect(fn.Results)
	return out
}

// importsForFile returns the sorted, de-duplicated import lines a generated file
// needs: the packages of its qualified decorators AND of the types in its
// reproduced wrapper signatures. Selectors are resolved against the source
// file's own imports first, then the package's //deco:import directives. Only
// referenced selectors are included, so generated files never carry an unused
// import.
func importsForFile(jobs []job, fileImports map[string]string, resolver importResolver) []string {
	used := map[string]bool{}
	for _, j := range jobs {
		for _, d := range j.decorators {
			for _, sel := range d.selectors {
				used[sel] = true
			}
		}
		for _, sel := range signatureSelectors(j.fn) {
			used[sel] = true
		}
	}
	seen := map[string]bool{}
	var lines []string
	for sel := range used {
		if line, ok, _ := resolver.resolve(sel, fileImports); ok && !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return lines
}

// moduleIndex is what one `go list ./...` run reveals about the enclosing
// module: its packages by name (for selector auto-resolution) and each
// package's source directory (for reading decorator signatures).
type moduleIndex struct {
	byName    map[string][]string // package name -> import path(s)
	dirByPath map[string]string   // import path -> source directory
}

// moduleIndexCache memoises the index per module root, so `go list` runs at
// most once even when many directories are processed in one invocation.
var moduleIndexCache = map[string]*moduleIndex{}

// moduleIndexFor returns the enclosing module's package index via `go list`.
// It is best-effort: if the toolchain is unavailable or the directory is
// outside a module, it returns an empty index and callers fall back to
// //deco:import. "main" packages are excluded (they can't be imported).
func moduleIndexFor(dir string) *moduleIndex {
	root := moduleRoot(dir)
	idx := &moduleIndex{byName: map[string][]string{}, dirByPath: map[string]string{}}
	if root == "" {
		return idx
	}
	if m, ok := moduleIndexCache[root]; ok {
		return m
	}
	moduleIndexCache[root] = idx // cache up front; an error leaves it empty (no retry)

	// -find skips dependency resolution (fast, and works on not-yet-built code);
	// -e keeps going past packages that don't load.
	cmd := exec.Command("go", "list", "-e", "-find", "-f", "{{.Name}}|{{.ImportPath}}|{{.Dir}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return idx
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		name, rest, ok := strings.Cut(line, "|")
		if !ok || name == "" || name == "main" {
			continue
		}
		path, pkgDir, _ := strings.Cut(rest, "|")
		if path == "" {
			continue
		}
		idx.byName[name] = append(idx.byName[name], path)
		if pkgDir != "" {
			idx.dirByPath[path] = pkgDir
		}
	}
	return idx
}

// moduleRoot returns the directory of the go.mod enclosing dir, or "" if none.
func moduleRoot(dir string) string {
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return ""
	}
	return filepath.Dir(gomod)
}

// fillReceiver populates a job's method fields from the declaration's
// receiver. The decorator chain is built over the METHOD EXPRESSION
// (e.g. (*Service).doImpl), whose type is a plain function taking the receiver
// as its first parameter — so any function decorator works on methods
// unchanged. Generic receivers are rejected: a type parameter prevents the
// package-level method expression the chain needs.
func fillReceiver(j *job, fn *ast.FuncDecl, fset *token.FileSet) error {
	field := fn.Recv.List[0]
	base := field.Type
	star := false
	if s, ok := base.(*ast.StarExpr); ok {
		star, base = true, s.X
	}
	ident, ok := base.(*ast.Ident)
	if !ok {
		pos := fset.Position(fn.Pos())
		return fmt.Errorf("%s:%d: cannot decorate method %q: generic receivers are not supported "+
			"(the package-level decorator chain is built over a method expression, "+
			"which a type parameter prevents)", pos.Filename, pos.Line, fn.Name.Name)
	}

	typeStr := typeString(field.Type, fset)
	name := ""
	if len(field.Names) == 1 && field.Names[0].Name != "_" {
		name = field.Names[0].Name
	} else {
		// An unnamed (or blank) receiver still needs a name so the wrapper can
		// forward it; pick one that no declared parameter uses.
		name = freshName("recv", declaredParamNames(fn.Type))
	}

	j.recvSig = name + " " + typeStr
	j.recvName = name
	j.recvType = ident.Name
	if star {
		j.recvExpr = "(" + typeStr + ")" // (*Service).doImpl
	} else {
		j.recvExpr = typeStr // Service.doImpl
	}
	return nil
}

// declaredParamNames returns the set of parameter names a signature declares.
func declaredParamNames(fn *ast.FuncType) map[string]bool {
	names := map[string]bool{}
	if fn.Params == nil {
		return names
	}
	for _, field := range fn.Params.List {
		for _, n := range field.Names {
			names[n.Name] = true
		}
	}
	return names
}

// freshName returns base, or base0, base1, … — the first not present in used.
func freshName(base string, used map[string]bool) string {
	name := base
	for i := 0; used[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}

// renameBytes returns the source file's bytes with each decorated function
// renamed to its impl name and a wrapper marker stamped above it. It edits text
// directly (using AST node offsets) instead of reprinting the AST, so untouched
// code, comments and formatting are preserved byte-for-byte. changed reports
// whether any rename was actually applied; inserted is the sorted, 1-based line
// numbers of the marker lines in the output (the source map for this file).
func renameBytes(path string, jobs []job, fset *token.FileSet) (out []byte, inserted []int, changed bool, err error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("reading %s: %w", path, err)
	}

	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	var srcFuncLines []int // source lines of funcs that get a marker inserted
	for _, j := range jobs {
		if !j.needsRename {
			continue
		}
		// Replace the function name identifier.
		nameStart := fset.Position(j.decl.Name.Pos()).Offset
		nameEnd := fset.Position(j.decl.Name.End()).Offset
		edits = append(edits, edit{nameStart, nameEnd, j.implName})

		// Insert the marker on its own line directly above the func keyword, so
		// it stays attached to the function as part of its doc on re-parse.
		// FuncDecl.Pos() is the func keyword (column 0 for a top-level func).
		funcStart := fset.Position(j.decl.Pos()).Offset
		edits = append(edits, edit{funcStart, funcStart, marker + j.wrapperName + "\n"})
		srcFuncLines = append(srcFuncLines, fset.Position(j.decl.Pos()).Line)
	}
	if len(edits) == 0 {
		return src, nil, false, nil
	}

	// Each marker adds exactly one line above its func. With source func lines
	// sorted ascending, the i-th (0-based) marker lands at output line
	// srcLine[i] + i, because i earlier markers already shifted everything down.
	sort.Ints(srcFuncLines)
	inserted = make([]int, len(srcFuncLines))
	for i, l := range srcFuncLines {
		inserted[i] = l + i
	}

	// Apply from the end of the file backwards so offsets stay valid. We do NOT
	// reformat: the edits are already gofmt-clean (a column-0 marker line and an
	// identifier swap), and skipping gofmt preserves the user's exact
	// //@decorate comments instead of letting gofmt rewrite them to "// @".
	sort.Slice(edits, func(i, k int) bool { return edits[i].start > edits[k].start })
	out = src
	for _, e := range edits {
		out = append(out[:e.start:e.start], append([]byte(e.text), out[e.end:]...)...)
	}
	return out, inserted, true, nil
}

// genTemplate renders one generated file. Functions are emitted in source
// order; the whole file is run through go/format afterwards, so exact
// whitespace here does not matter.
var genTemplate = template.Must(template.New("gen").Parse(`{{.Header}}

package {{.Package}}
{{if .Imports}}
import (
{{range .Imports}}	{{.}}
{{end}})
{{end}}
{{range .Funcs}}
{{if .MWMode}}
// {{.MWVar}} is {{.WrapperName}}'s fused middleware chain: every decorator is
// a middleware factory, so the whole stack is one slice built at package init
// and the wrapper below runs it with NO reflection — the middlewares wrap a
// typed call directly.
var {{.MWVar}} = []{{.MWPkg}}.Middleware{ {{.MWList}} }

func {{if .Recv}}({{.Recv}}) {{end}}{{.WrapperName}}({{.Params}}){{if .Results}} {{.Results}}{{end}} {
	{{.MWPkg}}.Run({{.MWVar}}, func() { {{if .ResultTargets}}{{.ResultTargets}} = {{end}}{{.Call}} })
{{if .ResultTargets}}	return
{{end}}}
{{else}}
// {{.ChainVar}} is {{.WrapperName}}'s decorator chain, built ONCE at package
// init (like Python's fn = a(b(fn))) — so construction-time side effects such
// as route registration run at startup, and the reflection wrappers are created
// only once rather than on every call.
var {{.ChainVar}} = {{.Chain}}

func {{if .Recv}}({{.Recv}}) {{end}}{{.WrapperName}}({{.Params}}){{if .Results}} {{.Results}}{{end}} {
	{{.Ret}}{{.ChainVar}}({{.CallArgs}})
}
{{end}}{{end}}`))

type genFunc struct {
	WrapperName string
	ChainVar    string // package-level var holding the built chain
	Recv        string // rendered receiver for a method wrapper ("" for functions)
	Params      string
	Results     string
	Ret         string // "return " or ""
	Chain       string // decorator chain expression over the impl
	CallArgs    string // forwarded argument list (receiver first for methods)

	// Fused (all-factory) mode: the wrapper runs the middleware slice over a
	// typed call, with no reflection.
	MWMode        bool
	MWVar         string // package-level []Middleware var, e.g. addImplMWs
	MWList        string // factory calls in annotation order (outermost first)
	MWPkg         string // selector for deco's decorators package
	Call          string // typed inner call, e.g. addImpl(a, b) or s.doImpl(x)
	ResultTargets string // named results the call assigns, e.g. "r0, r1"
}

type genData struct {
	Header  string
	Package string
	Imports []string
	Funcs   []genFunc
}

// genBytes builds the formatted contents of <base>_gen.go for one source
// file's jobs (in memory; no write). imports is the sorted set of import lines
// the generated code needs (for qualified decorators); mwSel is the selector
// for deco's decorators package when any chain in the file is fused.
func genBytes(srcPath, pkg string, jobs []job, imports []string, mwSel string, fset *token.FileSet) ([]byte, error) {
	data := genData{Header: generatedHeader, Package: pkg, Imports: imports}
	for _, j := range jobs {
		params, callArgs := renderParams(j.fn, fset)
		gf := genFunc{
			WrapperName: j.wrapperName,
			ChainVar:    j.implName + "Decorated",
			Params:      params,
			Results:     renderResults(j.fn, fset),
			Chain:       buildChain(j, mwSel),
			CallArgs:    callArgs,
		}
		if j.isMethod() {
			// The wrapper is a method; the chain (built over the method
			// expression) takes the receiver as its first argument. The chain
			// var is qualified by the receiver type, since methods of the same
			// name may exist on several types in one package.
			gf.Recv = j.recvSig
			gf.ChainVar = lowerFirstWord(j.recvType) + j.wrapperName + "Decorated"
			gf.CallArgs = j.recvName
			if callArgs != "" {
				gf.CallArgs = j.recvName + ", " + callArgs
			}
		}
		if j.fn.Results != nil && len(j.fn.Results.List) > 0 {
			gf.Ret = "return "
		}
		if j.allFactories() {
			// Reflection-free mode: run the middleware slice over a typed
			// call. The wrapper's results become named so the closure can
			// assign them; a short-circuiting middleware leaves the zero
			// values, matching decorators.Func semantics.
			gf.MWMode = true
			gf.MWPkg = mwSel
			gf.MWVar = strings.TrimSuffix(gf.ChainVar, "Decorated") + "MWs"
			gf.MWList = factoryCalls(j.decorators)
			gf.Call = j.implName + "(" + callArgs + ")"
			if j.isMethod() {
				gf.Call = j.recvName + "." + j.implName + "(" + callArgs + ")"
			}
			used := declaredParamNames(j.fn)
			if j.isMethod() {
				used[j.recvName] = true
			}
			gf.Results, gf.ResultTargets = renderNamedResults(j.fn, fset, used)
		}
		data.Funcs = append(data.Funcs, gf)
	}

	var buf bytes.Buffer
	if err := genTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering wrapper for %s: %w", srcPath, err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("formatting generated code for %s:\n%s\n%w", srcPath, buf.String(), err)
	}
	return formatted, nil
}

// buildChain assembles the decorator chain over the impl function. Decorators
// are applied BOTTOM-UP: the last annotation is the innermost wrapper and the
// first (topmost) annotation is the outermost. Leading arguments are passed
// before the wrapped function, per the contract.
//
// For //deco:wrap logged then //deco:wrap timing("slow") over Add, this yields
//
//	logged(timing("slow", addImpl))
//
// For a method the innermost expression is the METHOD EXPRESSION, e.g.
// logged((*Service).doImpl) — a plain func value whose first parameter is the
// receiver.
//
// Runs of consecutive middleware FACTORIES are fused into a single
// decorators.Chain call (one reflective layer for the whole run) with
// wrap-style decorators applied around them, so a mixed stack
// [wrap w, factory f1, factory f2] yields w(decorators.Chain(impl, f1(), f2())).
// (An all-factory stack never reaches this: genBytes emits the
// reflection-free Run wrapper instead.)
func buildChain(j job, mwSel string) string {
	expr := j.implName
	if j.isMethod() {
		expr = j.recvExpr + "." + j.implName
	}
	decs := j.decorators
	for i := len(decs) - 1; i >= 0; {
		if !decs[i].factory {
			d := decs[i]
			if d.args == "" {
				expr = fmt.Sprintf("%s(%s)", d.name, expr)
			} else {
				expr = fmt.Sprintf("%s(%s, %s)", d.name, d.args, expr)
			}
			i--
			continue
		}
		// Fuse the consecutive factory run decs[k..i]; Chain's mws[0] is
		// outermost, matching the topmost annotation of the run.
		k := i
		for k > 0 && decs[k-1].factory {
			k--
		}
		expr = fmt.Sprintf("%s.Chain(%s, %s)", mwSel, expr, factoryCalls(decs[k:i+1]))
		i = k - 1
	}
	return expr
}

// factoryCalls renders factory invocations in annotation order, e.g.
// `logged(), timing("slow")`.
func factoryCalls(decs []decorator) string {
	parts := make([]string, len(decs))
	for i, d := range decs {
		parts[i] = d.name + "(" + d.args + ")"
	}
	return strings.Join(parts, ", ")
}

// renderParams reproduces the parameter list verbatim (synthesising names where
// the original omitted them) and returns both the signature fragment and the
// matching forwarded-argument list. Variadic parameters are preserved and
// forwarded with `...`.
func renderParams(fn *ast.FuncType, fset *token.FileSet) (params, callArgs string) {
	var sigParts, argParts []string
	idx := 0
	for _, field := range fn.Params.List {
		typeStr := typeString(field.Type, fset)
		_, variadic := field.Type.(*ast.Ellipsis)

		if len(field.Names) == 0 {
			// Unnamed parameter — synthesise a stable name.
			name := fmt.Sprintf("p%d", idx)
			idx++
			sigParts = append(sigParts, name+" "+typeStr)
			argParts = append(argParts, forwardArg(name, variadic))
			continue
		}
		var names []string
		for _, n := range field.Names {
			name := n.Name
			if name == "_" {
				// Blank params still need a real name to be forwarded.
				name = fmt.Sprintf("p%d", idx)
			}
			idx++
			names = append(names, name)
			argParts = append(argParts, forwardArg(name, variadic))
		}
		sigParts = append(sigParts, strings.Join(names, ", ")+" "+typeStr)
	}
	return strings.Join(sigParts, ", "), strings.Join(argParts, ", ")
}

// forwardArg renders a single forwarded argument, appending `...` for the
// variadic parameter so the spread is preserved.
func forwardArg(name string, variadic bool) string {
	if variadic {
		return name + "..."
	}
	return name
}

// renderResults reproduces the result types. Names are dropped (they are
// irrelevant to a forwarding wrapper); multiple results are wrapped in parens.
func renderResults(fn *ast.FuncType, fset *token.FileSet) string {
	if fn.Results == nil || len(fn.Results.List) == 0 {
		return ""
	}
	var parts []string
	for _, field := range fn.Results.List {
		typeStr := typeString(field.Type, fset)
		// A field may carry several names sharing one type (e.g. (a, b int));
		// each contributes one result of that type.
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			parts = append(parts, typeStr)
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// renderNamedResults reproduces the result types with synthesised NAMES
// (r0, r1, …, dodging any colliding parameter or receiver name in used), so a
// fused wrapper's inner closure can assign them and a bare return hands them
// back. It returns the parenthesised signature fragment and the matching
// assignment-target list; both are empty for a void function.
func renderNamedResults(fn *ast.FuncType, fset *token.FileSet, used map[string]bool) (sig, targets string) {
	if fn.Results == nil || len(fn.Results.List) == 0 {
		return "", ""
	}
	var sigParts, tgt []string
	idx := 0
	for _, field := range fn.Results.List {
		typeStr := typeString(field.Type, fset)
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		var names []string
		for range n {
			name := freshName(fmt.Sprintf("r%d", idx), used)
			used[name] = true
			idx++
			names = append(names, name)
			tgt = append(tgt, name)
		}
		sigParts = append(sigParts, strings.Join(names, ", ")+" "+typeStr)
	}
	return "(" + strings.Join(sigParts, ", ") + ")", strings.Join(tgt, ", ")
}

// paramCount returns the number of value parameters declared by a func type.
func paramCount(fn *ast.FuncType) int {
	if fn.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fn.Params.List {
		if len(field.Names) == 0 {
			count++
		} else {
			count += len(field.Names)
		}
	}
	return count
}

// implName derives the unexported implementation name from a function name:
// the first rune is lower-cased and "Impl" is appended (Add -> addImpl).
func implName(name string) string {
	if name == "" {
		return "impl"
	}
	return lowerFirstWord(name) + "Impl"
}

// lowerFirstWord lower-cases a name's first rune (Service -> service).
func lowerFirstWord(name string) string {
	if name == "" {
		return name
	}
	r := []rune(name)
	r[0] = lower(r[0])
	return string(r)
}

func lower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// typeString renders a type expression to its Go source form using go/printer,
// which faithfully handles selectors, pointers, slices, maps, channels, func
// types, generics and ellipses.
func typeString(expr ast.Expr, fset *token.FileSet) string {
	return exprStringFset(expr, fset)
}

// exprString renders an expression parsed from an annotation spec (these have
// no real file positions, so a fresh empty FileSet is fine).
func exprString(expr ast.Expr) string {
	return exprStringFset(expr, token.NewFileSet())
}

func exprStringFset(expr ast.Expr, fset *token.FileSet) string {
	var buf bytes.Buffer
	// printer with no extra config emits compact, canonical type syntax.
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}
