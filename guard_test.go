package main

import "testing"

func TestParseGuard(t *testing.T) {
	r := parseGuard("# komentar\n5432=postgres-dev\n\n8000 = simas\nrusak\n=x\n9=\n")
	if len(r) != 2 || r[0].Port != "5432" || r[0].Owner != "postgres-dev" || r[1].Port != "8000" || r[1].Owner != "simas" {
		t.Fatalf("got %+v", r)
	}
}

func TestOwnerMatches(t *testing.T) {
	if !ownerMatches(PortItem{Process: "docker:postgres-dev"}, "postgres-dev") {
		t.Fatal("docker name")
	}
	if !ownerMatches(PortItem{Process: "php", Project: "Simas"}, "simas") {
		t.Fatal("project")
	}
	if ownerMatches(PortItem{Process: "node", Project: "x"}, "postgres-dev") {
		t.Fatal("harus tidak cocok")
	}
}

func TestCheckGuard(t *testing.T) {
	rules := []Rule{{Port: "5432", Owner: "postgres-dev"}}
	ok := []PortItem{{Port: "5432", Process: "docker:postgres-dev"}}
	bad := []PortItem{{Port: "5432", Process: "postgres", PID: "9"}, {Port: "80", Process: "nginx"}}
	if len(checkGuard(rules, ok)) != 0 {
		t.Fatal("pemilik sah")
	}
	v := checkGuard(rules, bad)
	if len(v) != 1 || v[0].Item.PID != "9" {
		t.Fatalf("got %+v", v)
	}
	if violationText(v) == "" || violationText(nil) != "" {
		t.Fatal("teks")
	}
}
