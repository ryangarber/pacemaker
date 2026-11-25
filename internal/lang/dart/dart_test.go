package dart

import (
	"testing"

	"github.com/ryangarber/pacemaker/internal/lang"
)

const src = `import 'package:x/x.dart';

/// Docs.
class Auth extends Base {
  final String _token;
  Auth(this._token);
  Auth.named() : _token = '' { print('\${1} {'); }

  Future<bool> login(String u) async {
    if (u.isEmpty) { return false; }
    return true;
  }

  String get token => _token;
}

enum Color { red, green }

extension on String {
  bool get blank => trim().isEmpty;
}

void main() {}
`

func TestSymbols(t *testing.T) {
	syms := Module{}.Symbols([]byte(src))
	want := map[string]string{
		"Auth": "class", "Auth._token": "field", "Auth.Auth": "constructor", "Auth.named": "constructor",
		"Auth.login": "method", "Auth.token": "getter", "Color": "enum", "Color.red": "value",
		"on String.blank": "getter", "main": "function",
	}
	got := map[string]lang.Symbol{}
	for _, s := range syms {
		got[s.Name] = s
	}
	for name, kind := range want {
		if s, ok := got[name]; !ok || s.Kind != kind {
			t.Errorf("symbol %s: got %+v, want kind %s (all: %v)", name, s, kind, syms)
		}
	}
	if s := got["Auth.login"]; s.Start != 9 || s.End != 12 {
		t.Errorf("Auth.login span = %d-%d, want 9-12", s.Start, s.End)
	}
	if s := got["Auth"]; s.Start != 3 {
		t.Errorf("class span should include doc comment, got start %d", s.Start)
	}
	if got["Auth._token"].Exported {
		t.Error("_token should be private")
	}
}

func TestNormalizeCosmetic(t *testing.T) {
	a := "int f(int a, int b) {\n  return a; // c\n}\n"
	b := "/// doc\nint f(\n  int a,\n  int b,\n) {\n  return a;\n}\n"
	if string(Module{}.Normalize([]byte(a))) != string(Module{}.Normalize([]byte(b))) {
		t.Error("formatting/comment/trailing-comma changes should normalise equal")
	}
}

func TestCompare(t *testing.T) {
	old := "class A {\n  void a(int x) {}\n  void b() {}\n  void _c() {}\n}\n"
	cur := "class A {\n  void a(int x, int y) {}\n  void _c() { print(1); }\n  void d() {}\n}\n"
	kinds := map[string]string{}
	for _, f := range (Module{}).Compare([]byte(old), []byte(cur)) {
		kinds[f.Symbol] = f.Kind
	}
	if kinds["A.a"] != "signature-changed" || kinds["A.b"] != "renamed" && kinds["A.b"] != "removed" ||
		kinds["A._c"] != "body-changed" {
		t.Errorf("unexpected findings: %v", kinds)
	}
}
