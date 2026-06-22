package transpiler

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScanFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const nexusSrc = `package handlers

//@provide
func NewUsersService() *int { return nil }

//@rest GET /users/:id
func NewGetUser() error { return nil }

//@mutation
//@auth Requires("ADMIN")
func NewCreateUser() error { return nil }

//@ws /events chat.send
//@auth Required
func NewChatSend() error { return nil }

// not annotated
func helper() {}
`

func TestScan_NexusAnnotations(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "handlers.go", nexusSrc)

	hits, err := Scan(dir, "rest", "query", "mutation", "subscription", "ws", "worker", "provide", "auth")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	type want struct {
		fn, kw string
		args   []string
	}
	wants := []want{
		{"NewUsersService", "provide", nil},
		{"NewGetUser", "rest", []string{"GET", "/users/:id"}},
		{"NewCreateUser", "mutation", nil},
		{"NewCreateUser", "auth", []string{`Requires("ADMIN")`}},
		{"NewChatSend", "ws", []string{"/events", "chat.send"}},
		{"NewChatSend", "auth", []string{"Required"}},
	}
	if len(hits) != len(wants) {
		t.Fatalf("got %d hits, want %d: %+v", len(hits), len(wants), hits)
	}
	for i, w := range wants {
		h := hits[i]
		if h.Func != w.fn || h.Keyword != w.kw {
			t.Fatalf("hit %d: got %s/%s, want %s/%s", i, h.Func, h.Keyword, w.fn, w.kw)
		}
		if len(h.Args) != len(w.args) {
			t.Fatalf("hit %d args: got %v, want %v", i, h.Args, w.args)
		}
		for j := range w.args {
			if h.Args[j] != w.args[j] {
				t.Fatalf("hit %d arg %d: got %q, want %q", i, j, h.Args[j], w.args[j])
			}
		}
		if h.Pkg != "handlers" {
			t.Fatalf("hit %d pkg = %q, want handlers", i, h.Pkg)
		}
		if h.Pos.Line == 0 {
			t.Fatalf("hit %d has no position", i)
		}
	}
}

func TestScan_KeywordFilter(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "h.go", nexusSrc)

	hits, err := Scan(dir, "rest")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Keyword != "rest" || hits[0].Func != "NewGetUser" {
		t.Fatalf("filter: got %+v", hits)
	}
}

func TestScan_NoFilterReturnsAll(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "h.go", nexusSrc)
	hits, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	// provide, rest, mutation, auth, ws, auth = 6 directives total.
	if len(hits) != 6 {
		t.Fatalf("no-filter should return all 6 directives, got %d: %+v", len(hits), hits)
	}
}

func TestScan_AtPrefixOnFilterTolerated(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "h.go", nexusSrc)
	a, err := Scan(dir, "@rest")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Scan(dir, "rest")
	if len(a) != len(b) || len(a) != 1 {
		t.Fatalf("@rest and rest must behave identically: %d vs %d", len(a), len(b))
	}
}

func TestScan_RecursesAndSkipsGeneratedAndTests(t *testing.T) {
	root := t.TempDir()
	// top package
	writeScanFile(t, root, "a.go", "package app\n\n//@rest GET /a\nfunc NewA() error { return nil }\n")
	// nested package
	writeScanFile(t, filepath.Join(root, "sub"), "b.go", "package sub\n\n//@query\nfunc NewB() error { return nil }\n")
	// must be ignored
	writeScanFile(t, root, "z_gen.go", "package app\n\n//@rest GET /gen\nfunc NewGen() error { return nil }\n")
	writeScanFile(t, root, "a_test.go", "package app\n\n//@rest GET /test\nfunc NewT() error { return nil }\n")
	writeScanFile(t, filepath.Join(root, "testdata"), "c.go", "package td\n\n//@rest GET /td\nfunc NewTD() error { return nil }\n")

	hits, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (a.go + sub/b.go), got %d: %+v", len(hits), hits)
	}
	// Deterministic order: root dir before sub dir.
	if hits[0].Func != "NewA" || hits[1].Func != "NewB" {
		t.Fatalf("order/contents wrong: %+v", hits)
	}
}

func TestScan_HonorsWrapperMarker(t *testing.T) {
	// A function deco already wrapped: the impl is renamed and a //deco:wrapper
	// marker carries the public name. Scan should report the public name.
	dir := t.TempDir()
	writeScanFile(t, dir, "h.go", "package h\n\n//deco:wrapper NewGetUser\n//@rest GET /u\nfunc newGetUserImpl() error { return nil }\n")
	hits, err := Scan(dir, "rest")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Func != "NewGetUser" {
		t.Fatalf("wrapper marker not honored: %+v", hits)
	}
}
