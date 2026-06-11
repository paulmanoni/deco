package main

import (
	"bytes"
	"io"
	"os/exec"
	"slices"
	"strings"
	"testing"
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
		"build":   true,
		"test":    true,
		"vet":     true,
		"install": true,
		"list":    true,
		"mod":     true, // arbitrary/unenumerated → still forwarded
		"env":     true,
		"future":  true, // a hypothetical future go subcommand
		"generate": false,
		"help":     false,
		"completion": false,
		"":           false,
		"-h":         false,
		"--help":     false,
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
	overlayProvider = func() (string, func(), error) {
		return "/tmp/fake-overlay.json", func() { cleaned = true }, nil
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

	overlayProvider = func() (string, func(), error) {
		t.Fatal("overlay must not be built for a non-compile subcommand")
		return "", nil, nil
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

// TestDiagFilter checks the read-vs-run seam: stderr streams through unchanged,
// and a reference to a generated (*_gen.go) file flips the warn flag.
func TestDiagFilter(t *testing.T) {
	var buf bytes.Buffer
	f := &diagFilter{w: &buf}
	io.WriteString(f, "example/math.go:9:2: ordinary diagnostic\n")
	if f.sawTranspiled {
		t.Error("a plain source reference should not be flagged")
	}
	io.WriteString(f, "example/math_gen.go:6:9: vet: something\n")
	f.flush()
	if !f.sawTranspiled {
		t.Error("a _gen.go reference should be flagged")
	}
	if !strings.Contains(buf.String(), "math_gen.go:6:9") {
		t.Error("filter must pass content through unchanged")
	}
}
