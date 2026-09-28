package transpiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenameOnlyTouchesDeclaration is the guard for the precise-text-edit
// rename: renaming `func Add` must replace ONLY the declaration's name
// identifier (located by its AST byte offset), never a same-spelled identifier,
// string literal, comment, or call site.
func TestRenameOnlyTouchesDeclaration(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

// Address must survive — it merely contains the substring "Add".
func Address() string {
	msg := "Add this to the list" // the word Add appears in a string and a comment
	return msg
}

//@decorate logged
func Add(x int) int {
	return x
}

func use() int { return Add(1) }
`
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	orig := readFile(t, filepath.Join(dir, "code.go"))
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))

	// The declaration — and only the declaration — is renamed.
	if !strings.Contains(orig, "func addImpl(x int) int") {
		t.Errorf("declaration not renamed to addImpl:\n%s", orig)
	}
	if strings.Contains(orig, "func Add(") {
		t.Errorf("original still declares `func Add(`:\n%s", orig)
	}

	// Everything else that merely spells "Add" must be byte-for-byte intact.
	for _, want := range []string{
		"func Address() string",                          // a different identifier
		`"Add this to the list"`,                         // a string literal
		"the word Add appears in a string and a comment", // a comment
		"return Add(1)",                                  // a call site (still hits the wrapper)
		"//deco:wrapper Add",                             // idempotency marker stamped
	} {
		if !strings.Contains(orig, want) {
			t.Errorf("expected original to still contain %q:\n%s", want, orig)
		}
	}

	// The generated wrapper recreates the public name with the same signature.
	if !strings.Contains(gen, "func Add(x int) int") {
		t.Errorf("generated wrapper missing `func Add(x int) int`:\n%s", gen)
	}
}

// TestIdempotent re-runs Generate and asserts the output is byte-identical and
// the function was not renamed a second time (no addImplImpl).
func TestIdempotent(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

//@decorate logged
func Add(a, b int) int { return a + b }
`
	code := filepath.Join(dir, "code.go")
	gen := filepath.Join(dir, "code_gen.go")
	writeFile(t, code, src)

	if err := Generate(dir); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	first := readFile(t, code) + "\x00" + readFile(t, gen)

	if err := Generate(dir); err != nil {
		t.Fatalf("second Generate: %v", err)
	}
	second := readFile(t, code) + "\x00" + readFile(t, gen)

	if first != second {
		t.Errorf("Generate is not idempotent.\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	if strings.Contains(readFile(t, code), "ImplImpl") {
		t.Errorf("double rename produced an ImplImpl name:\n%s", readFile(t, code))
	}
}

// TestSignatureForwarding covers the tricky signatures: no result, multiple
// results, and variadic forwarding.
func TestSignatureForwarding(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

//@decorate logged
func Nothing(s string) {}

//@decorate logged
func Two(a, b int) (int, error) { return a + b, nil }

//@decorate logged
func Sum(nums ...int) int { return 0 }
`
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))

	for _, want := range []string{
		"func Nothing(s string) {", // no result: no `return`, no parens
		"func Two(a, b int) (int, error)",
		"func Sum(nums ...int) int",
		"sumImplDecorated(nums...)", // variadic forwarded with ...
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated code missing %q:\n%s", want, gen)
		}
	}
	if strings.Contains(gen, "func Nothing(s string) {\n\treturn") {
		t.Errorf("void function should not `return` its chain call:\n%s", gen)
	}
}

// TestRecursiveCallReentersWrapper pins the (intended) behaviour that a
// recursive decorated function re-enters the decorator chain on each self-call:
// only the declaration is renamed, so the inner self-call keeps the original
// name and resolves to the wrapper — never the impl. This matches Python's
// semantics (the name binds to the decorated function).
func TestRecursiveCallReentersWrapper(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

//@decorate logged
func Fact(n int) int {
	if n <= 1 {
		return 1
	}
	return n * Fact(n-1)
}
`
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	orig := readFile(t, filepath.Join(dir, "code.go"))
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))

	// The declaration is renamed...
	if !strings.Contains(orig, "func factImpl(n int) int") {
		t.Errorf("declaration not renamed:\n%s", orig)
	}
	// ...but the recursive self-call is NOT — it still names Fact, so it
	// re-enters the wrapper rather than calling factImpl directly.
	if !strings.Contains(orig, "Fact(n-1)") {
		t.Errorf("recursive self-call should remain Fact(n-1):\n%s", orig)
	}
	if strings.Contains(orig, "factImpl(n-1)") {
		t.Errorf("recursive self-call must NOT be rewritten to the impl:\n%s", orig)
	}
	if !strings.Contains(gen, "func Fact(n int) int") {
		t.Errorf("generated wrapper missing func Fact:\n%s", gen)
	}
}

// TestCustomAnnotation checks WithAnnotation: a custom keyword is honoured, and
// the default keyword does not match it.
// TestMethodDecorators covers method support: the method is renamed in place
// (receiver kept), the chain is built over the METHOD EXPRESSION so plain
// function decorators work unchanged, the wrapper is a same-signature method
// forwarding the receiver, chain vars are qualified by receiver type (two
// types may share a method name), and an unnamed receiver gets a synthesised
// name. Then: idempotency, and the generated package must actually compile.
func TestMethodDecorators(t *testing.T) {
	dir := t.TempDir()
	src := `package p

func logged[F any](fn F) F { return fn }

type Service struct{ n int }

//deco:wrap logged
func (s *Service) Do(x int) int { return s.n + x }

type Job struct{}

//deco:wrap logged
func (Job) Do(x int) int { return x * 2 }
`
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	orig := readFile(t, filepath.Join(dir, "code.go"))
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))

	// The rename keeps the receiver and stamps the marker.
	for _, want := range []string{
		"//deco:wrapper Do\nfunc (s *Service) doImpl(x int) int",
		"//deco:wrapper Do\nfunc (Job) doImpl(x int) int",
	} {
		if !strings.Contains(orig, want) {
			t.Errorf("transformed original missing %q:\n%s", want, orig)
		}
	}

	// The chain is built over the method expression; chain vars are qualified
	// by receiver type; the wrapper is a method that forwards the receiver.
	for _, want := range []string{
		"var serviceDoDecorated = logged((*Service).doImpl)",
		"func (s *Service) Do(x int) int",
		"return serviceDoDecorated(s, x)",
		"var jobDoDecorated = logged(Job.doImpl)",
		"func (recv Job) Do(x int) int",
		"return jobDoDecorated(recv, x)",
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated wrapper missing %q:\n%s", want, gen)
		}
	}

	// Idempotent: a second run must not rename again or change any output.
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate (rerun): %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "code.go")); got != orig {
		t.Errorf("rerun changed the original:\n%s", got)
	}
	if got := readFile(t, filepath.Join(dir, "code_gen.go")); got != gen {
		t.Errorf("rerun changed the wrapper:\n%s", got)
	}

	// The result must compile.
	writeFile(t, filepath.Join(dir, "go.mod"), "module methodtest\n\ngo 1.27.1\n")
	if out, err := goBuild(dir); err != nil {
		t.Fatalf("generated package does not compile: %v\n%s", err, out)
	}
}

// TestMethodGenericReceiverRejected: a type parameter prevents the
// package-level method expression the chain needs, so it must be a clear error.
func TestMethodGenericReceiverRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "code.go"), `package p

func logged[F any](fn F) F { return fn }

type Box[T any] struct{ v T }

//deco:wrap logged
func (b *Box[T]) Get() T { return b.v }
`)
	err := Generate(dir)
	if err == nil || !strings.Contains(err.Error(), "generic receivers are not supported") {
		t.Fatalf("expected a generic-receiver error, got: %v", err)
	}
}

// TestWrapDirective checks the primary //deco:wrap directive form: it is
// recognised by default, coexists with the //@decorate alias in one doc group,
// survives a custom alias keyword, and never collides with the //deco:wrapper
// marker (word-boundary matching).
func TestWrapDirective(t *testing.T) {
	src := `package p

func logged[F any](fn F) F { return fn }

func timed[F any](fn F) F { return fn }

//deco:wrap logged
//@decorate timed
func Add(a, b int) int { return a + b }
`
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))
	if !strings.Contains(gen, "logged(timed(addImpl))") {
		t.Errorf("directive and alias should stack (logged outermost):\n%s", gen)
	}

	// Idempotent: the re-parse must read //deco:wrapper as the marker, never as
	// a //deco:wrap directive naming a "per Add" decorator.
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate (rerun): %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "code.go")); strings.Contains(got, "ImplImpl") {
		t.Errorf("marker misread as directive on rerun:\n%s", got)
	}

	// The directive keeps working under a custom alias keyword.
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "code.go"), `package p

func logged[F any](fn F) F { return fn }

//deco:wrap logged
func Add(a, b int) int { return a + b }
`)
	if err := Generate(dir2, WithAnnotation("@wrap")); err != nil {
		t.Fatalf("Generate with alias: %v", err)
	}
	if gen := readFile(t, filepath.Join(dir2, "code_gen.go")); !strings.Contains(gen, "logged(addImpl)") {
		t.Errorf("//deco:wrap should be recognised alongside a custom alias:\n%s", gen)
	}
}

func TestCustomAnnotation(t *testing.T) {
	src := `package p

func logged[F any](fn F) F { return fn }

//@wrap logged
func Add(a, b int) int { return a + b }
`
	// With the matching keyword, the wrapper is generated.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "code.go"), src)
	if err := Generate(dir, WithAnnotation("@wrap")); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	gen := readFile(t, filepath.Join(dir, "code_gen.go"))
	if !strings.Contains(gen, "func Add(a, b int) int") {
		t.Errorf("custom annotation @wrap not honoured:\n%s", gen)
	}

	// With the default keyword, //@wrap is just an ordinary comment.
	dir2 := t.TempDir()
	writeFile(t, filepath.Join(dir2, "code.go"), src)
	if err := Generate(dir2); err != nil {
		t.Fatalf("Generate (default): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir2, "code_gen.go")); err == nil {
		t.Error("default @decorate should not match //@wrap, but a wrapper was generated")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// goBuild compiles the package in dir with the real toolchain.
func goBuild(dir string) (string, error) {
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
