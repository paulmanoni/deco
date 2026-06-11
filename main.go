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

// overlayProvider produces deco's overlay for the current directory tree. It is
// a package var so tests can substitute a fake.
var overlayProvider = func() (path string, cleanup func(), err error) {
	return transpiler.Overlay(".", opts()...)
}

// runChild executes the assembled command. A package var so tests can intercept
// it without spawning go.
var runChild = func(cmd *exec.Cmd) error { return cmd.Run() }

// passThrough forwards `go <sub> <userArgs…>`, injecting deco's overlay for the
// compile/run subcommands, and returns the child's exit code. Overlay cleanup
// always runs (deferred), even on failure or panic.
func passThrough(sub string, userArgs []string) int {
	var overlayPath string
	if overlaySubcommands[sub] {
		path, cleanup, err := overlayProvider()
		if err != nil {
			fmt.Fprintln(os.Stderr, "deco:", err)
			return 1
		}
		defer cleanup() // always remove the temp overlay, even on panic/failure
		overlayPath = path
	}

	cmd := exec.Command("go", buildGoArgs(sub, overlayPath, userArgs)...)
	cmd.Env = os.Environ() // preserve GOFLAGS, CGO_ENABLED, GOOS/GOARCH, caches…
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout

	// `run` streams raw so the program stays interactive. The diagnostic
	// commands route stderr through a filter that flags transpiled positions.
	var filter *diagFilter
	if overlayPath != "" && sub != "run" {
		filter = &diagFilter{w: os.Stderr}
		cmd.Stderr = filter
	} else {
		cmd.Stderr = os.Stderr
	}

	err := runChild(cmd)

	if filter != nil {
		filter.flush()
		if filter.sawTranspiled {
			fmt.Fprintln(os.Stderr,
				"deco: note: positions above refer to deco's transpiled output (e.g. *_gen.go);\n"+
					"      they may not line up with your original source.")
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

// diagFilter streams a child's stderr through unchanged while detecting
// references to deco's transpiled output, so deco can warn that reported
// positions may not match the user's source.
//
// SEAM: this is exactly where a future source-map pass slots in. To remap, it
// would buffer each complete line, rewrite "<gen-file>:line:col" back to the
// original source position using a position table built during transpilation,
// then write. Today it must NOT delay output (so wrapped programs stay
// responsive), so it writes first and only scans whole lines for detection.
type diagFilter struct {
	w             io.Writer
	acc           []byte
	sawTranspiled bool
}

func (f *diagFilter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p) // stream immediately
	f.acc = append(f.acc, p...)
	for {
		i := bytes.IndexByte(f.acc, '\n')
		if i < 0 {
			break
		}
		f.detect(f.acc[:i])
		f.acc = f.acc[i+1:]
	}
	return n, err
}

func (f *diagFilter) flush() {
	if len(f.acc) > 0 {
		f.detect(f.acc)
		f.acc = nil
	}
}

func (f *diagFilter) detect(line []byte) {
	if !f.sawTranspiled && bytes.Contains(line, []byte("_gen.go")) {
		f.sawTranspiled = true
	}
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
