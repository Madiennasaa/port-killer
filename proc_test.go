package main

import (
	"reflect"
	"testing"
)

func TestParseCmdline(t *testing.T) {
	got := parseCmdline([]byte("node\x00server.js\x00--port\x003000\x00"))
	want := []string{"node", "server.js", "--port", "3000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if parseCmdline(nil) != nil {
		t.Fatal("nil should give nil")
	}
	if parseCmdline([]byte{}) != nil {
		t.Fatal("empty should give nil")
	}
	got = parseCmdline([]byte("a\x00\x00b\x00"))
	want = []string{"a", "", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty arg: got %q want %q", got, want)
	}
}

func TestFindProjectRoot(t *testing.T) {
	cases := []struct {
		name   string
		files  map[string]bool
		path   string
		home   string
		expect string
	}{
		{"gomod", map[string]bool{"/home/u/app/go.mod": true}, "/home/u/app/sub", "", "app"},
		{"git", map[string]bool{"/srv/x/.git": true}, "/srv/x/a/b", "", "x"},
		{"composer", map[string]bool{"/srv/y/composer.json": true}, "/srv/y/public", "", "y"},
		{"package", map[string]bool{"/srv/z/package.json": true}, "/srv/z/src", "", "z"},
		{"fallback", map[string]bool{}, "/home/u/none", "", "none"},
		{"root", map[string]bool{}, "/", "", "/"},
		{"home-stop", map[string]bool{"/home/ubai02/.git": true}, "/home/ubai02/sideproj", "/home/ubai02", "sideproj"},
		{"sudo-home", map[string]bool{"/home/ubai02/.git": true}, "/home/ubai02/sideproj", "/root", "ubai02"},
		{"home-nested", map[string]bool{"/home/ubai02/.git": true, "/home/ubai02/sideproj/go.mod": true}, "/home/ubai02/sideproj/sub", "/home/ubai02", "sideproj"},
	}
	for _, c := range cases {
		exists := func(p string) bool { return c.files[p] }
		if got := findProjectRootFrom(c.path, exists, c.home); got != c.expect {
			t.Errorf("%s: got %q want %q", c.name, got, c.expect)
		}
	}
}
