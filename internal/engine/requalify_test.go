package engine

import "testing"

func TestRequalify(t *testing.T) {
	for _, c := range [][3]string{
		{"AuthService.login", "login", "AuthService.login"},
		{"AuthService.login", "AuthService.logout", "AuthService.logout"},
		{"A.b", "c", "A.c"},
		{"LoginCtrl.$scope.login", "$scope.login", "LoginCtrl.$scope.login"},
		{"LoginCtrl", "LoginCtrl.$scope.user", "LoginCtrl.$scope.user"},
		{"helper", "helper", "helper"},
		{"helper", "other", "other"},
	} {
		if got := requalify(c[0], c[1]); got != c[2] {
			t.Errorf("requalify(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
