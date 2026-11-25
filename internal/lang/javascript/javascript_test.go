package javascript

import (
	"testing"
)

const src = "import { a } from './a'\n" +
	"export interface Opts { url: string; get(x: number): void }\n" +
	"export class Client<T> extends Base {\n" +
	"  #secret = 1\n" +
	"  private base = '/api'\n" +
	"  constructor(private opts: Opts) { super() }\n" +
	"  async request<R>(path: string): Promise<R> {\n" +
	"    const re = /}{/g\n" +
	"    const t = `x ${ {a: 1}.a } }`\n" +
	"    return fetch(path)\n" +
	"  }\n" +
	"  delete(id: string) { return id }\n" +
	"}\n" +
	"export const useThing = (x: number) => {\n" +
	"  return x\n" +
	"}\n" +
	"app.use(foo)\n" +
	"function helper() {}\n"

func TestSymbols(t *testing.T) {
	got := map[string]string{}
	exp := map[string]bool{}
	spans := map[string][2]int{}
	for _, s := range (Module{}).Symbols([]byte(src)) {
		got[s.Name] = s.Kind
		exp[s.Name] = s.Exported
		spans[s.Name] = [2]int{s.Start, s.End}
	}
	want := map[string]string{
		"Opts": "interface", "Opts.url": "field", "Opts.get": "method", "Client": "class",
		"Client.#secret": "field", "Client.base": "field", "Client.constructor": "constructor",
		"Client.request": "method", "Client.delete": "method", "useThing": "function", "helper": "function",
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("%s: got kind %q, want %q (all: %v)", n, got[n], k, got)
		}
	}
	if _, ok := got["use"]; ok {
		t.Error("expression statement app.use(...) must not be a symbol")
	}
	if spans["Client.request"] != [2]int{7, 11} {
		t.Errorf("request span = %v (regex/template literal braces confused the parser?)", spans["Client.request"])
	}
	if exp["Client.base"] || exp["Client.#secret"] || !exp["useThing"] || exp["helper"] {
		t.Errorf("visibility wrong: %v", exp)
	}
}

func TestNormalizePrettier(t *testing.T) {
	a := "const x = { a: 'b', c: 1 };\nfoo(x);\n"
	b := "const x = {\n  a: \"b\",\n  c: 1,\n}\nfoo(x)  // call\n"
	if string(Module{}.Normalize([]byte(a))) != string(Module{}.Normalize([]byte(b))) {
		t.Error("quote style, semicolons, trailing commas and comments should normalise equal")
	}
}
