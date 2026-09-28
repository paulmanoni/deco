package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/paulmanoni/deco/transpiler"
)

func TestBuildGoArgs(t *testing.T) {
	// Compile/run subcommand: overlay injected before the user args, which are
	// forwarded verbatim and in order (flags and packages alike).
	got := buildGoArgs("test", "/tmp/ov.json", []string{"-race", "-run", "TestX", "./..."})
	want := []string{"test", "-overlay", "/tmp/ov.json", "-race", "-run", "TestX", "./..."}
	if !slices.Equal(got, want) {
		t.Errorf("with overlay: got %v, want %v", got, want)
	}

	// No overlay (e.g. `deco env`): args forwarded with no -overlay added.
	got = buildGoArgs("env", "", []string{"GOVERSION"})
	want = []string{"env", "GOVERSION"}
	if !slices.Equal(got, want) {
		t.Errorf("without overlay: got %v, want %v", got, want)
	}
}

func TestIsPassThrough(t *testing.T) {
	cases := map[string]bool{
		"build":    true,
		"test":     true,
		"vet":      true,
		"install":  true,
		"list":     true,
		"mod":      true, // arbitrary/unenumerated → still forwarded
		"env":      true,
		"future":   true, // a hypothetical future go subcommand
		"generate": false,
		"help":     false,
		"":         false,
		"-h":       false,
		"--help":   false,
	}
	for sub, want := range cases {
		if got := isPassThrough(sub); got != want {
			t.Errorf("isPassThrough(%q) = %v, want %v", sub, got, want)
		}
	}
}

func TestSplitLeading(t *testing.T) {
	ann, sub, rest := splitLeading([]string{"--annotation", "@wrap", "test", "-race", "./..."})
	if ann != "@wrap" || sub != "test" || !slices.Equal(rest, []string{"-race", "./..."}) {
		t.Errorf("got (%q, %q, %v)", ann, sub, rest)
	}

	// --annotation=VALUE form.
	ann, sub, rest = splitLeading([]string{"--annotation=@mw", "build", "./x"})
	if ann != "@mw" || sub != "build" || !slices.Equal(rest, []string{"./x"}) {
		t.Errorf("got (%q, %q, %v)", ann, sub, rest)
	}

	// Single-dash forms, go flag style.
	ann, sub, rest = splitLeading([]string{"-annotation", "@mw", "vet", "./..."})
	if ann != "@mw" || sub != "vet" || !slices.Equal(rest, []string{"./..."}) {
		t.Errorf("single-dash: got (%q, %q, %v)", ann, sub, rest)
	}
	ann, sub, _ = splitLeading([]string{"-annotation=@mw", "build"})
	if ann != "@mw" || sub != "build" {
		t.Errorf("single-dash =: got (%q, %q)", ann, sub)
	}

	// No deco flags: default annotation, subcommand is the first token, args verbatim.
	ann, sub, rest = splitLeading([]string{"vet", "./..."})
	if ann != "@decorate" || sub != "vet" || !slices.Equal(rest, []string{"./..."}) {
		t.Errorf("got (%q, %q, %v)", ann, sub, rest)
	}
}

func TestExitCodeFromErr(t *testing.T) {
	if c := exitCodeFromErr(nil); c != 0 {
		t.Errorf("nil err → %d, want 0", c)
	}
	// A real child that exits non-zero must map to its exact code.
	err := exec.Command("sh", "-c", "exit 7").Run()
	if c := exitCodeFromErr(err); c != 7 {
		t.Errorf("exit 7 → %d, want 7", c)
	}
}

// TestPassThroughOverlayAndCleanup covers the must-haves at once: overlay is
// injected into the exec'd command, the user's args are forwarded verbatim,
// cleanup runs even when the child FAILS, and the child's exit code propagates.
func TestPassThroughOverlayAndCleanup(t *testing.T) {
	origProvider, origRunner := overlayProvider, runChild
	defer func() { overlayProvider, runChild = origProvider, origRunner }()

	cleaned := false
	overlayProvider = func() (string, *transpiler.SourceMap, func(), error) {
		return "/tmp/fake-overlay.json", nil, func() { cleaned = true }, nil
	}
	var gotArgs []string
	runChild = func(cmd *exec.Cmd) error {
		gotArgs = cmd.Args
		return exec.Command("sh", "-c", "exit 2").Run() // simulate `go test` failing
	}

	code := passThrough("test", []string{"-race", "./..."})

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (child failure must propagate)", code)
	}
	if !cleaned {
		t.Error("overlay cleanup did not run on failure")
	}
	want := []string{"go", "test", "-overlay", "/tmp/fake-overlay.json", "-race", "./..."}
	if !slices.Equal(gotArgs, want) {
		t.Errorf("exec'd args = %v, want %v", gotArgs, want)
	}
}

// TestPassThroughArbitraryNoOverlay confirms a non-compile subcommand is
// forwarded verbatim with NO overlay (and the overlay is never built).
func TestPassThroughArbitraryNoOverlay(t *testing.T) {
	origProvider, origRunner := overlayProvider, runChild
	defer func() { overlayProvider, runChild = origProvider, origRunner }()

	overlayProvider = func() (string, *transpiler.SourceMap, func(), error) {
		t.Fatal("overlay must not be built for a non-compile subcommand")
		return "", nil, nil, nil
	}
	var gotArgs []string
	runChild = func(cmd *exec.Cmd) error { gotArgs = cmd.Args; return nil }

	if code := passThrough("env", []string{"GOVERSION"}); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	want := []string{"go", "env", "GOVERSION"}
	if !slices.Equal(gotArgs, want) {
		t.Errorf("exec'd args = %v, want %v", gotArgs, want)
	}
}

// TestDiagFilterRemap checks the source-map remapping the go toolchain triggers:
// it reports SHADOW paths, which must be rewritten to the real source path (with
// the line remapped for transformed originals), generated wrappers keep their
// line but get the logical path and a flag, and unknown files pass through.
// Absolute paths outside the test cwd keep displayPath from rewriting them.
func TestDiagFilterRemap(t *testing.T) {
	orig := "/proj/example/math.go"    // transformed original, one marker at line 9
	gen := "/proj/example/math_gen.go" // fully generated
	shadowOrig := "/tmp/ov/0_math.go"  // what `go` actually reports
	shadowGen := "/tmp/ov/1_math_gen.go"
	sm := transpiler.NewSourceMap(
		map[string][]int{orig: {9}},
		[]string{gen},
		map[string]string{shadowOrig: orig, shadowGen: gen},
	)

	var buf bytes.Buffer
	f := newDiagFilter(&buf, sm)
	io.WriteString(f, shadowOrig+":10:2: undefined: foo\n") // shadow line 10 → source math.go:9
	io.WriteString(f, shadowGen+":6:9: vet: oops\n")        // generated → logical path, line kept
	io.WriteString(f, "/proj/other.go:3:1: untouched\n")    // unknown → unchanged
	f.flush()

	out := buf.String()
	if !strings.Contains(out, orig+":9:2: undefined: foo") {
		t.Errorf("shadow position not remapped to source math.go:9:\n%s", out)
	}
	if strings.Contains(out, "0_math.go") {
		t.Errorf("shadow path leaked into output:\n%s", out)
	}
	if !strings.Contains(out, gen+":6:9: vet: oops") {
		t.Errorf("generated shadow should map to logical _gen.go path, line kept:\n%s", out)
	}
	if !strings.Contains(out, "/proj/other.go:3:1: untouched") {
		t.Errorf("unknown file should pass through unchanged:\n%s", out)
	}
	if !f.sawGenerated {
		t.Error("a generated-file reference should set sawGenerated")
	}
}

// TestDiagFilterStdoutPositions covers the stdout cases `go test` emits: an
// indented failure line and a tab-indented panic stack frame, where the
// position is mid-line (not at the start) and a shadow path is used.
func TestDiagFilterStdoutPositions(t *testing.T) {
	orig := "/proj/calc.go"       // func at source line 11, body at 12
	shadow := "/tmp/ov/0_calc.go" // overlay shifts body to line 13
	sm := transpiler.NewSourceMap(
		map[string][]int{orig: {11}}, // one marker inserted at line 11
		nil,
		map[string]string{shadow: orig},
	)

	var buf bytes.Buffer
	f := newDiagFilter(&buf, sm)
	io.WriteString(f, "    "+shadow+":13: assertion failed\n") // indented test failure
	io.WriteString(f, "\t"+shadow+":13 +0x30\n")               // panic stack frame
	f.flush()

	out := buf.String()
	// 13 → 12, shadow → source path; leading whitespace and trailing text kept.
	if !strings.Contains(out, "    "+orig+":12: assertion failed") {
		t.Errorf("indented failure not remapped:\n%q", out)
	}
	if !strings.Contains(out, "\t"+orig+":12 +0x30") {
		t.Errorf("stack frame not remapped:\n%q", out)
	}
	if strings.Contains(out, "0_calc.go") {
		t.Errorf("shadow path leaked:\n%q", out)
	}
}

// TestRunGenerateFlags covers the gofmt-style flag vocabulary: -l lists the
// files that would change without writing them, the default mode writes, and a
// second -l on an up-to-date tree lists nothing. The fixture uses the
// //deco:wrap directive form end to end.
func TestRunGenerateFlags(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

//deco:wrap logged
func Add(a, b int) int { return a + b }
`
	path := dir + "/code.go"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// -l: both the transformed original and the new wrapper are listed, and
	// nothing is written.
	var out, errb bytes.Buffer
	if code := runGenerate([]string{"-l", dir}, &out, &errb); code != 0 {
		t.Fatalf("generate -l = %d, stderr:\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "code.go") || !strings.Contains(out.String(), "code_gen.go") {
		t.Errorf("-l should list code.go and code_gen.go, got:\n%s", out.String())
	}
	if got, _ := os.ReadFile(path); string(got) != src {
		t.Error("-l must not modify the source file")
	}
	if _, err := os.Stat(dir + "/code_gen.go"); err == nil {
		t.Error("-l must not write the wrapper")
	}

	// Default mode writes, exactly like before.
	out.Reset()
	errb.Reset()
	if code := runGenerate([]string{dir}, &out, &errb); code != 0 {
		t.Fatalf("generate = %d, stderr:\n%s", code, errb.String())
	}
	gen, err := os.ReadFile(dir + "/code_gen.go")
	if err != nil {
		t.Fatalf("wrapper not written: %v", err)
	}
	if !strings.Contains(string(gen), "func Add(a, b int) int") {
		t.Errorf("wrapper missing decorated Add:\n%s", gen)
	}

	// Up to date now: -l lists nothing.
	out.Reset()
	if code := runGenerate([]string{"-l", dir}, &out, &errb); code != 0 {
		t.Fatalf("generate -l (2nd) = %d", code)
	}
	if out.Len() != 0 {
		t.Errorf("-l on an up-to-date tree should print nothing, got:\n%s", out.String())
	}
}

// TestPassThroughRefusesSingleFileOnDecoratedPackage: `deco run x.go` on a
// decorated package can never work (go compiles only the listed files, so the
// generated wrappers are missing) — it must fail with the run-the-package
// hint instead of go's baffling "undefined" errors. A file in an undecorated
// package still forwards, keeping go-parity.
func TestPassThroughRefusesSingleFileOnDecoratedPackage(t *testing.T) {
	origProvider, origRunner := overlayProvider, runChild
	defer func() { overlayProvider, runChild = origProvider, origRunner }()

	sm := transpiler.NewSourceMap(nil, []string{mustAbs(t, "example/main_gen.go")}, nil)
	overlayProvider = func() (string, *transpiler.SourceMap, func(), error) {
		return "/tmp/fake-overlay.json", sm, func() {}, nil
	}
	ran := false
	runChild = func(cmd *exec.Cmd) error { ran = true; return nil }

	if code := passThrough("run", []string{"example/main.go"}); code != 1 {
		t.Errorf("single file on decorated package: exit = %d, want 1", code)
	}
	if ran {
		t.Error("go must not be invoked for a refused single-file run")
	}

	// A file whose package the transpiler didn't touch forwards as normal.
	if code := passThrough("run", []string{"other/tool.go"}); code != 0 || !ran {
		t.Errorf("undecorated package: exit = %d, ran = %v; want 0, true", code, ran)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestLocalDir(t *testing.T) {
	for in, want := range map[string]string{
		"example": "./example", ".": ".", "./x": "./x", "/abs/p": "/abs/p",
	} {
		if got := localDir(in); got != want {
			t.Errorf("localDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecoVersion(t *testing.T) {
	if got := decoVersion(); !strings.HasPrefix(got, "deco version ") {
		t.Errorf("decoVersion() = %q, want a 'deco version …' line", got)
	}
}

func TestHasJSONFlag(t *testing.T) {
	if !hasJSONFlag([]string{"-race", "-json", "./..."}) {
		t.Error("-json not detected")
	}
	if hasJSONFlag([]string{"-race", "./..."}) {
		t.Error("false positive for -json")
	}
}
