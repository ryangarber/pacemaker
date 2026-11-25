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

const ngSrc = `(function () {
  'use strict';
  angular.module('app', []);

  angular.module('app')
    .controller('LoginCtrl', ['$scope', 'Auth', function ($scope, Auth) {
      $scope.user = null;
      $scope.login = function (name) {
        $scope.busy = true;
      };
      $scope.$watch('user', function () {});
    }])
    .factory('Auth', function ($http) {
      return { login: login, logout: function () {} };
      function login(name) { return $http.post('/login', name); }
    });

  angular.module('app').controller('ProfileCtrl', ProfileCtrl);
  function ProfileCtrl($scope) {
    var vm = this;
    vm.save = save;
    vm.reset = function () {};
    function save() {}
    function helper() {}
  }
  angular.module('app').component('userCard', {
    controller: function () { this.$onInit = function () {}; }
  });
})();
function Store() { this.items = []; }
Store.prototype.add = function (x) { this.items.push(x); };
exports.make = function () {};
function plain() { this.notASymbol = 1; $scope.loose = 2; }
const nums = [1, 2].filter(function (x) { return x; });
`

func TestAngularSymbols(t *testing.T) {
	got := map[string]string{}
	exp := map[string]bool{}
	spans := map[string][2]int{}
	for _, s := range (Module{}).Symbols([]byte(ngSrc)) {
		got[s.Name] = s.Kind
		exp[s.Name] = s.Exported
		spans[s.Name] = [2]int{s.Start, s.End}
	}
	want := map[string]string{
		"LoginCtrl": "controller", "LoginCtrl.$scope.user": "field", "LoginCtrl.$scope.login": "method",
		"Auth": "factory", "Auth.login": "function", "Auth.logout": "method",
		"ProfileCtrl": "controller", "ProfileCtrl.save": "function", "ProfileCtrl.reset": "method", "ProfileCtrl.helper": "function",
		"userCard": "component", "userCard.controller": "method", "userCard.controller.$onInit": "method",
		"Store": "function", "Store.items": "field", "Store.add": "method", "make": "function",
		"plain": "function", "plain.$scope.loose": "field",
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("%s: got kind %q, want %q", n, got[n], k)
		}
	}
	for _, n := range []string{"LoginCtrl.$scope.busy", "plain.notASymbol", "module", "default", "angular.module", "filter"} {
		if _, ok := got[n]; ok {
			t.Errorf("%s must not be a symbol (all: %v)", n, got)
		}
	}
	if spans["LoginCtrl.$scope.login"] != [2]int{8, 10} {
		t.Errorf("$scope.login span = %v", spans["LoginCtrl.$scope.login"])
	}
	if !exp["Auth.login"] || !exp["ProfileCtrl.save"] || exp["ProfileCtrl.helper"] || !exp["ProfileCtrl"] {
		t.Errorf("visibility wrong: %v", exp)
	}
}

func TestAngularSemanticDiff(t *testing.T) {
	a := "angular.module('m').controller('C', function ($scope) {\n  $scope.go = function (a) { return a; };\n  $scope.old = 1;\n});\n"
	b := "angular.module('m').controller('C', function ($scope) {\n  $scope.go = function (a, b) { return a; };\n  $scope.added = 2;\n});\n"
	kinds := map[string]string{}
	for _, f := range (Module{}).Compare([]byte(a), []byte(b)) {
		kinds[f.Kind+" "+f.Symbol] = string(f.Severity)
	}
	for _, k := range []string{"signature-changed C.$scope.go", "removed C.$scope.old", "added C.$scope.added"} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("missing finding %q (got %v)", k, kinds)
		}
	}
}
