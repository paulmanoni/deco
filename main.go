// Command deco is a comment-hosted decorator transpiler that brings
// Python-style decorators to Go via code generation — and a thin wrapper around
// the go toolchain.
//
//	deco generate [dir]   // deco-native: rename originals + write <file>_gen.go
//	deco <go-subcommand>  // run `go <subcommand>` with deco's transpile overlay
//
// `generate` materialises wrappers on disk. Every other subcommand is forwarded
// to the real `go` toolchain: for the compile/run subcommands
// (build, run, test, vet, install, list) deco first produces its transpiled
// overlay and injects `-overlay`, so your source tree is never modified; all
// other subcommands (env, version, mod, …) are forwarded untouched. deco never
// reimplements go behaviour — it transpiles, then hands off.
//
// deco's own flags (currently --annotation) go BEFORE the subcommand:
//
//	deco --annotation "@wrap" test -race ./...
//
// See README.md for the full model.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paulmanoni/deco/transpiler"
)

// annotation is the doc-comment keyword that marks a decorator; configurable
// via --annotation so teams can use //@wrap, //@apply, etc.
var annotation string

// opts builds the transpiler options from the current flags.
func opts() []transpiler.Option {
	return []transpiler.Option{transpiler.WithAnnotation(annotation)}
}

func main() {
	// Anything that isn't `generate`/help is forwarded to the go toolchain, so
	// arbitrary (and future) subcommands work without being enumerated.
	ann, sub, rest := splitLeading(os.Args[1:])
	if isPassThrough(sub) {
		annotation = ann
		os.Exit(passThrough(sub, rest))
	}
	// cobra owns generate + help/completion (and parses --annotation itself).
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// splitLeading consumes deco's own flags appearing before the subcommand and
// returns them plus the subcommand and the remaining (verbatim) args. It does
// not touch anything from the subcommand onward — those belong to go.
func splitLeading(args []string) (annotation, sub string, rest []string) {
	annotation = "@decorate"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--annotation":
			if i+1 < len(args) {
				annotation = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--annotation="):
			annotation = strings.TrimPrefix(a, "--annotation=")
		default:
			// First non-deco-flag token: the subcommand; the rest is forwarded.
			return annotation, a, args[i+1:]
		}
	}
	return annotation, "", nil
}

// isPassThrough reports whether a subcommand should be forwarded to go. Empty
// input, flags (e.g. -h), and deco's own commands are handled by cobra instead.
func isPassThrough(sub string) bool {
	if sub == "" || strings.HasPrefix(sub, "-") {
		return false
	}
	switch sub {
	case "generate", "help", "completion":
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
	cmd.Stdout = os.Stdout

	// `run` streams raw so the program stays interactive. The diagnostic
	// commands route stderr through a filter that remaps transpiled positions
	// back to the user's source.
	var filter *diagFilter
	if overlayPath != "" && sub != "run" {
		filter = newDiagFilter(os.Stderr, sm)
		cmd.Stderr = filter
	} else {
		cmd.Stderr = os.Stderr
	}

	err := runChild(cmd)

	if filter != nil {
		filter.flush()
		if filter.sawGenerated {
			fmt.Fprintln(os.Stderr,
				"deco: note: some positions above are in generated wrappers (*_gen.go),\n"+
					"      which have no source equivalent. Other positions were mapped back to your source.")
		}
	}
	return exitCodeFromErr(err)
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

// diagLine matches a Go diagnostic position at the start of a line:
// "<path>.go:<line>[:col][: message]". The path is non-greedy so it stops at
// the first ".go", not a later one inside the message.
var diagLine = regexp.MustCompile(`^(.+?\.go):(\d+)(:.*)?$`)

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

// remap rewrites the line's leading position. A transformed original's shadow
// path + transpiled line becomes the real source path + source line; a
// generated wrapper keeps its line but its shadow path becomes the logical
// *_gen.go path (and is flagged for the note); unknown files are left alone.
func (f *diagFilter) remap(line []byte) []byte {
	m := diagLine.FindSubmatch(line)
	if m == nil {
		return line
	}
	ln, err := strconv.Atoi(string(m[2]))
	if err != nil {
		return line
	}
	srcPath, srcLine, generated, known := f.sm.Remap(string(m[1]), ln)
	if !known {
		return line
	}
	if generated {
		f.sawGenerated = true
		return []byte(displayPath(srcPath) + ":" + string(m[2]) + string(m[3]))
	}
	return []byte(displayPath(srcPath) + ":" + strconv.Itoa(srcLine) + string(m[3]))
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

// rootCmd wires up the cobra command tree (deco-native commands only; toolchain
// subcommands are dispatched in main before cobra runs).
func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "deco",
		Short: "Comment-hosted decorator transpiler and go toolchain wrapper",
		Long: "deco brings Python-style decorators to Go via code generation.\n\n" +
			"Annotate any plain function with doc comments:\n\n" +
			"  //@decorate logged\n" +
			"  //@decorate timing(\"slow\")\n" +
			"  func Add(a, b int) int { return a + b }\n\n" +
			"Commands:\n" +
			"  generate [dir]     write <file>_gen.go wrappers to disk\n" +
			"  build|run|test|vet|install|list [args]\n" +
			"                     run the matching `go` command with deco's overlay\n" +
			"  <any go subcommand> [args]\n" +
			"                     forwarded to `go` verbatim (env, version, mod, …)\n\n" +
			"Run `deco <cmd>` instead of `go <cmd>`. deco's own flags go before the\n" +
			"subcommand, e.g. `deco --annotation \"@wrap\" test ./...`.",
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&annotation, "annotation", "@decorate",
		"doc-comment keyword that marks a decorator, e.g. @decorate or @wrap")
	root.AddCommand(generateCmd())
	return root
}

func generateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate [dir]",
		Short: "Rename annotated funcs and write <file>_gen.go wrappers to disk",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDir("generate", argOrDot(args))
			if err != nil {
				return err
			}
			if err := transpiler.Generate(dir, opts()...); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "deco: generated wrappers in", dir)
			return nil
		},
	}
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
