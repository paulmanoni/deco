// Command deco is a comment-hosted decorator transpiler that brings
// Python-style decorators to Go via code generation — and a thin wrapper around
// the go toolchain.
//
//	deco generate [dir]   // deco-native: rename originals + write <file>_gen.go
//	deco <go-subcommand>  // run `go <subcommand>` with deco's transpile overlay
//
// `generate` materialises wrappers on disk (gofmt-style -l/-d/-w flags).
// Every other subcommand is forwarded
// to the real `go` toolchain: for the compile/run subcommands
// (build, run, test, vet, install, list) deco first produces its transpiled
// overlay and injects `-overlay`, so your source tree is never modified; all
// other subcommands (env, version, mod, …) are forwarded untouched. deco never
// reimplements go behaviour — it transpiles, then hands off.
//
// deco's own flags (currently -annotation) go BEFORE the subcommand:
//
//	deco -annotation "@wrap" test -race ./...
//
// See README.md for the full model.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/paulmanoni/deco/transpiler"
)

// annotation is the alias doc-comment keyword that marks a decorator (the
// //deco:wrap directive is always recognised); configurable via -annotation so
// teams can use //@wrap, //@apply, etc.
var annotation string

// opts builds the transpiler options from the current flags.
func opts() []transpiler.Option {
	return []transpiler.Option{transpiler.WithAnnotation(annotation)}
}

func main() {
	// Anything that isn't `generate`/help is forwarded to the go toolchain, so
	// arbitrary (and future) subcommands work without being enumerated.
	ann, sub, rest := splitLeading(os.Args[1:])
	annotation = ann
	switch {
	case isPassThrough(sub):
		os.Exit(passThrough(sub, rest))
	case sub == "generate":
		os.Exit(runGenerate(rest, os.Stdout, os.Stderr))
	default: // "", help, -h, --help
		usage(os.Stdout)
	}
}

// splitLeading consumes deco's own flags appearing before the subcommand and
// returns them plus the subcommand and the remaining (verbatim) args. It does
// not touch anything from the subcommand onward — those belong to go. Both
// -annotation and --annotation are accepted, in the go flag style.
func splitLeading(args []string) (annotation, sub string, rest []string) {
	annotation = "@decorate"
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			a = a[1:] // --annotation and -annotation alike
		}
		switch {
		case a == "-annotation":
			if i+1 < len(args) {
				annotation = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-annotation="):
			annotation = strings.TrimPrefix(a, "-annotation=")
		default:
			// First non-deco-flag token: the subcommand; the rest is forwarded.
			return annotation, args[i], args[i+1:]
		}
	}
	return annotation, "", nil
}

// isPassThrough reports whether a subcommand should be forwarded to go. Empty
// input, flags (e.g. -h), and deco's own commands are handled locally instead.
func isPassThrough(sub string) bool {
	if sub == "" || strings.HasPrefix(sub, "-") {
		return false
	}
	switch sub {
	case "generate", "help":
		return false
	default:
		return true
	}
}

// overlaySubcommands are the go subcommands that compile or run code, so deco
// injects its transpile overlay for them. Every other subcommand is forwarded
// unchanged (no overlay).
var overlaySubcommands = map[string]bool{
	"build":   true,
	"run":     true,
	"test":    true,
	"vet":     true,
	"install": true,
	"list":    true,
}

// overlayProvider produces deco's overlay (and its source map) for the current
// directory tree. It is a package var so tests can substitute a fake.
var overlayProvider = func() (path string, sm *transpiler.SourceMap, cleanup func(), err error) {
	return transpiler.OverlayWithSourceMap(".", opts()...)
}

// runChild executes the assembled command. A package var so tests can intercept
// it without spawning go.
var runChild = func(cmd *exec.Cmd) error { return cmd.Run() }

// passThrough forwards `go <sub> <userArgs…>`, injecting deco's overlay for the
// compile/run subcommands, and returns the child's exit code. Overlay cleanup
// always runs (deferred), even on failure or panic.
func passThrough(sub string, userArgs []string) int {
	var overlayPath string
	var sm *transpiler.SourceMap
	if overlaySubcommands[sub] {
		path, m, cleanup, err := overlayProvider()
		if err != nil {
			fmt.Fprintln(os.Stderr, "deco:", err)
			return 1
		}
		defer cleanup() // always remove the temp overlay, even on panic/failure
		overlayPath = path
		sm = m
	}

	cmd := exec.Command("go", buildGoArgs(sub, overlayPath, userArgs)...)
	cmd.Env = os.Environ() // preserve GOFLAGS, CGO_ENABLED, GOOS/GOARCH, caches…
	cmd.Stdin = os.Stdin

	// Remap transpiled positions back to source. stderr carries compiler/vet
	// diagnostics for every overlay subcommand (except `run`, which streams its
	// program raw). `go test` additionally prints failure positions and panic
	// stacks to stdout, so filter that too — but never under -json, whose
	// machine output must pass through verbatim.
	var stderrFilter, stdoutFilter *diagFilter
	if overlayPath != "" && sub != "run" {
		stderrFilter = newDiagFilter(os.Stderr, sm)
		cmd.Stderr = stderrFilter
	} else {
		cmd.Stderr = os.Stderr
	}
	if sub == "test" && !hasJSONFlag(userArgs) {
		stdoutFilter = newDiagFilter(os.Stdout, sm)
		cmd.Stdout = stdoutFilter
	} else {
		cmd.Stdout = os.Stdout
	}

	err := runChild(cmd)

	sawGenerated := false
	for _, fl := range []*diagFilter{stderrFilter, stdoutFilter} {
		if fl != nil {
			fl.flush()
			sawGenerated = sawGenerated || fl.sawGenerated
		}
	}
	if sawGenerated {
		fmt.Fprintln(os.Stderr,
			"deco: note: some positions above are in generated wrappers (*_gen.go),\n"+
				"      which have no source equivalent. Other positions were mapped back to your source.")
	}
	return exitCodeFromErr(err)
}

// hasJSONFlag reports whether the user requested machine-readable JSON output
// (go test -json / go list -json), which deco must not rewrite.
func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "-json" || a == "--json" {
			return true
		}
	}
	return false
}

// buildGoArgs assembles the argument vector for the go child: the subcommand,
// the injected -overlay flag (when set), then the user's args verbatim.
func buildGoArgs(sub, overlayPath string, userArgs []string) []string {
	args := []string{sub}
	if overlayPath != "" {
		args = append(args, "-overlay", overlayPath)
	}
	return append(args, userArgs...)
}

// exitCodeFromErr mirrors the child's exit status exactly: a clean run is 0, an
// *exec.ExitError yields the child's own code, and anything else (couldn't
// start go, etc.) is reported as 1.
func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "deco: %v\n", err)
	return 1
}

// diagPos matches a Go position "<path>.go:<line>" anywhere in a line — at the
// start (compiler/vet diagnostics), indented (test failures like
// "    foo_test.go:12: ..."), or in a panic stack frame
// ("\t/abs/foo.go:42 +0x..."). The path is the run of non-space characters
// ending in ".go".
var diagPos = regexp.MustCompile(`(\S+\.go):(\d+)`)

// diagFilter line-buffers a child's stderr and rewrites positions that point
// into deco's transpiled overlay back to the user's original source, using the
// transpiler's SourceMap. Generated wrappers (*_gen.go) have no source line, so
// those are left as-is and flagged for a closing note. Lines without a known
// transpiled position pass through untouched.
//
// This is the source-map seam the pass-through layer was designed around: all
// rewriting happens in remap, fed by transpiler.SourceMap.
type diagFilter struct {
	w            io.Writer
	sm           *transpiler.SourceMap
	acc          []byte
	sawGenerated bool
}

func newDiagFilter(w io.Writer, sm *transpiler.SourceMap) *diagFilter {
	return &diagFilter{w: w, sm: sm}
}

func (f *diagFilter) Write(p []byte) (int, error) {
	f.acc = append(f.acc, p...)
	for {
		i := bytes.IndexByte(f.acc, '\n')
		if i < 0 {
			break
		}
		f.emit(f.acc[:i], true)
		f.acc = f.acc[i+1:]
	}
	return len(p), nil
}

func (f *diagFilter) flush() {
	if len(f.acc) > 0 {
		f.emit(f.acc, false)
		f.acc = nil
	}
}

func (f *diagFilter) emit(line []byte, newline bool) {
	f.w.Write(f.remap(line))
	if newline {
		f.w.Write([]byte{'\n'})
	}
}

// remap rewrites every transpiled position in the line. A transformed
// original's shadow path + transpiled line becomes the real source path +
// source line; a generated wrapper keeps its line but its shadow path becomes
// the logical *_gen.go path (and is flagged for the note); unknown files (test
// files, untouched sources) are left alone. Any trailing ":col: message" sits
// outside the match and is preserved as-is.
func (f *diagFilter) remap(line []byte) []byte {
	return diagPos.ReplaceAllFunc(line, func(match []byte) []byte {
		sub := diagPos.FindSubmatch(match)
		ln, err := strconv.Atoi(string(sub[2]))
		if err != nil {
			return match
		}
		srcPath, srcLine, generated, known := f.sm.Remap(string(sub[1]), ln)
		if !known {
			return match
		}
		if generated {
			f.sawGenerated = true
			return []byte(displayPath(srcPath) + ":" + string(sub[2]))
		}
		return []byte(displayPath(srcPath) + ":" + strconv.Itoa(srcLine))
	})
}

// displayPath shows an absolute path relative to the cwd when it sits beneath
// it (matching how go prints source paths), else the absolute path.
func displayPath(abs string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return abs
}

// usage prints the top-level help, in the plain style of `go help`.
func usage(w io.Writer) {
	fmt.Fprint(w, `deco brings Python-style decorators to Go via code generation.

Annotate any plain function with doc comments:

	//deco:wrap logged
	//deco:wrap timing("slow")
	func Add(a, b int) int { return a + b }

Usage:

	deco [-annotation keyword] <command> [arguments]

Commands:

	generate [-l] [-d] [-w] [dir]
	                   rename annotated funcs and write <file>_gen.go wrappers
	                   (-l lists files that would change, -d prints diffs)
	build|run|test|vet|install|list [args]
	                   run the matching go command with deco's overlay
	<any go subcommand> [args]
	                   forwarded to go verbatim (env, version, mod, …)

Run 'deco <cmd>' instead of 'go <cmd>'. deco's own flags go before the
subcommand:

	deco -annotation @wrap test ./...

The -annotation keyword is an alias for the //deco:wrap directive, which is
always recognised.
`)
}

// runGenerate implements `deco generate`, with gofmt's flag vocabulary: by
// default it writes the results to disk; -l lists the files whose content
// would change, -d prints unified diffs, and either suppresses writing unless
// -w is also given.
func runGenerate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("deco generate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	list := fs.Bool("l", false, "list files whose generated content differs from disk")
	diff := fs.Bool("d", false, "display diffs instead of rewriting files")
	write := fs.Bool("w", false, "write results to disk (the default when -l and -d are absent)")
	fs.StringVar(&annotation, "annotation", annotation,
		"alias keyword that marks a decorator, e.g. @decorate or @wrap")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: deco generate [-l] [-d] [-w] [-annotation keyword] [dir]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}

	dir, err := resolveDir("generate", argOrDot(fs.Args()))
	if err != nil {
		fmt.Fprintln(stderr, "deco:", err)
		return 1
	}
	outputs, err := transpiler.Transform(dir, opts()...)
	if err != nil {
		fmt.Fprintln(stderr, "deco:", err)
		return 1
	}

	doWrite := *write || (!*list && !*diff)
	wrote := 0
	for _, o := range outputs {
		onDisk, readErr := os.ReadFile(o.Path)
		if readErr == nil && bytes.Equal(onDisk, o.Content) {
			continue // already up to date
		}
		if *list {
			fmt.Fprintln(stdout, displayPath(o.Path))
		}
		if *diff {
			if err := printDiff(stdout, o.Path, readErr == nil, o.Content); err != nil {
				fmt.Fprintln(stderr, "deco:", err)
				return 1
			}
		}
		if doWrite {
			if err := os.WriteFile(o.Path, o.Content, 0o644); err != nil {
				fmt.Fprintf(stderr, "deco: writing %s: %v\n", o.Path, err)
				return 1
			}
			wrote++
		}
	}
	if doWrite && wrote > 0 {
		fmt.Fprintln(stderr, "deco: generated wrappers in", dir)
	}
	return 0
}

// printDiff shows a unified diff between the on-disk file (or nothing, when it
// does not exist yet) and the generated content, labelled with the file's
// display path — the same shape gofmt -d prints.
func printDiff(w io.Writer, path string, exists bool, generated []byte) error {
	tmp, err := os.CreateTemp("", "deco-diff-*.go")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(generated); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	oldPath := os.DevNull
	if exists {
		oldPath = path
	}
	name := displayPath(path)
	cmd := exec.Command("diff", "-u",
		"-L", name+" (on disk)", "-L", name+" (generated)",
		oldPath, tmp.Name())
	cmd.Stdout = w
	err = cmd.Run()
	// diff exits 1 when the files differ — that is the expected case here.
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == 1 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("diff %s: %w", name, err)
	}
	return nil
}

// argOrDot returns the single positional argument, defaulting to ".".
func argOrDot(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return "."
}

// resolveDir maps the argument to the package directory deco should scan. Like
// `go run`, it accepts either a directory or a .go file; a file resolves to its
// containing directory (deco always works on the whole package).
func resolveDir(cmd, arg string) (string, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return "", fmt.Errorf("cannot access %q: %w", arg, err)
	}
	if info.IsDir() {
		return arg, nil
	}
	if strings.HasSuffix(arg, ".go") {
		if d := filepath.Dir(arg); d != "" {
			return d, nil
		}
		return ".", nil
	}
	return "", fmt.Errorf("%q is neither a directory nor a .go file; deco %s scans a package directory", arg, cmd)
}
