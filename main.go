// Command deco is a comment-hosted decorator transpiler that brings
// Python-style decorators to Go via code generation.
//
//	deco generate [dir]   // materialise: rename originals + write <file>_gen.go
//	deco build    [dir]   // overlay-build (source untouched): go build ./...
//	deco run      [dir]   // overlay-build (source untouched): go run .
//
// generate writes real files to disk. build and run instead use Go's
// `-overlay` mechanism: the renamed originals and generated wrappers are
// produced in a temp directory and injected into the build, so your source
// tree is never modified and no _gen.go is left behind.
//
// The directory argument may also be a .go file (it resolves to its package
// directory), mirroring `go run`. It defaults to ".". See README.md for the
// full model and workflow.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/paulmanoni/deco/transpiler"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Cobra has already printed the error; just signal failure.
		os.Exit(1)
	}
}

// annotation is the doc-comment keyword that marks a decorator; configurable
// via the --annotation flag so teams can use //@wrap, //@apply, etc.
var annotation string

// opts builds the transpiler options from the current flags.
func opts() []transpiler.Option {
	return []transpiler.Option{transpiler.WithAnnotation(annotation)}
}

// rootCmd wires up the cobra command tree.
func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "deco",
		Short: "Comment-hosted decorator transpiler for Go",
		Long: "deco brings Python-style decorators to Go via code generation.\n\n" +
			"Annotate any plain function with doc comments:\n\n" +
			"  //@decorate logged\n" +
			"  //@decorate timing(\"slow\")\n" +
			"  func Add(a, b int) int { return a + b }\n\n" +
			"deco renames the original and generates a type-matched wrapper with the\n" +
			"same name, so every caller transparently hits the decorator chain.\n\n" +
			"Use --annotation to pick a different keyword (default \"@decorate\").",
		SilenceUsage: true, // don't dump usage on a runtime error
	}
	root.PersistentFlags().StringVar(&annotation, "annotation", "@decorate",
		"doc-comment keyword that marks a decorator, e.g. @decorate or @wrap")
	root.AddCommand(generateCmd(), buildCmd(), runCmd())
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

func buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build [dir]",
		Short: "Overlay-build (source untouched), then: go build ./...",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return overlayExec("build", argOrDot(args), "build", "./...")
		},
	}
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [dir]",
		Short: "Overlay-build (source untouched), then: go run .",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return overlayExec("run", argOrDot(args), "run", ".")
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

// overlayExec transpiles into a shadow overlay (leaving the source untouched)
// and execs `go <sub> -overlay <file> <goArgs…>` in the resolved package dir.
// When the go subprocess exits non-zero, deco exits with that same code.
func overlayExec(cmd, arg, sub string, goArgs ...string) error {
	dir, err := resolveDir(cmd, arg)
	if err != nil {
		return err
	}
	overlay, cleanup, err := transpiler.Overlay(dir, opts()...)
	if err != nil {
		return err
	}

	full := append([]string{sub, "-overlay", overlay}, goArgs...)
	code := runGo(dir, full...)
	cleanup() // remove the temp overlay dir before we (possibly) exit
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

// runGo execs the go toolchain in dir, streaming stdio, and returns its exit
// code.
func runGo(dir string, args ...string) int {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "deco: running go: %v\n", err)
		return 1
	}
	return 0
}

// resolveDir maps the argument to the package directory deco should scan. Like
// `go run`, it accepts either a directory or a .go file; a file resolves to its
// containing directory (deco always works on the whole package, never a lone
// file, so that decorator references and other package files are visible).
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
