package main

import (
	"reflect"
	"testing"
)

func TestParseComposePorts(t *testing.T) {
	data := "services:\n  db:\n    ports:\n      - \"5432:5432\"\n      - 127.0.0.1:8080:80/tcp\n      - 3000:3000 # web\n    volumes:\n      - ./data:/var/lib\n      - /abs:/x\n"
	want := []string{"5432", "8080", "3000"}
	if got := parseComposePorts(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestParseEnvPorts(t *testing.T) {
	got := parseEnvPorts("# c\nAPP_PORT=8000\nDB_PORT=\"3306\"\nNAME=x\nBAD_PORT=abc\n")
	if !reflect.DeepEqual(got, []string{"8000", "3306"}) {
		t.Fatalf("got %v", got)
	}
}

func TestConflictLines(t *testing.T) {
	l := conflictLines([]Need{{"5432", ".env"}, {"8000", ".env"}}, []PortItem{{Port: "5432", Process: "docker:db", PID: "x"}})
	if len(l) != 2 || l[0][:10] != " CONFLICT " || l[1][:5] != " free" {
		t.Fatalf("got %v", l)
	}
	if len(conflictLines(nil, nil)) != 1 {
		t.Fatal("empty message")
	}
}
