package transpiler

import (
	"os"
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
		"func Address() string",                            // a different identifier
		`"Add this to the list"`,                           // a string literal
		"the word Add appears in a string and a comment",   // a comment
		"return Add(1)",                                    // a call site (still hits the wrapper)
		"//deco:wrapper Add",                               // idempotency marker stamped
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
		"func Nothing(s string) {",     // no result: no `return`, no parens
		"func Two(a, b int) (int, error)",
		"func Sum(nums ...int) int",
		"sumImplDecorated(nums...)",    // variadic forwarded with ...
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated code missing %q:\n%s", want, gen)
		}
	}
	if strings.Contains(gen, "func Nothing(s string) {\n\treturn") {
		t.Errorf("void function should not `return` its chain call:\n%s", gen)
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
