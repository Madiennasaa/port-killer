package main

import (
	"strings"
	"testing"
)

// TestMCPHistoryLimitClamped: a negative limit made historyEvents call
// make() with a negative capacity, crashing the whole server; zero returned
// nothing. Both now mean the default, and huge values are capped.
func TestMCPHistoryLimitClamped(t *testing.T) {
	withTempHome(t)
	for _, lim := range []string{"-1", "0", "1000000000"} {
		text, isErr := callText(t, mcpServer{}, "port_history", `{"limit":`+lim+`}`)
		if isErr || !strings.Contains(text, "events") {
			t.Errorf("limit %s: %q", lim, text)
		}
	}
	if got := mcpLimit(0); got != 20 {
		t.Errorf("mcpLimit(0) = %d", got)
	}
	if got := mcpLimit(1 << 40); got != maxEvents {
		t.Errorf("mcpLimit(huge) = %d", got)
	}
	if got := mcpLimit(7); got != 7 {
		t.Errorf("mcpLimit(7) = %d", got)
	}
	if text, isErr := callText(t, mcpServer{}, "port_history", `{"port":"x"}`); !isErr || !strings.Contains(text, "bad port") {
		t.Errorf("bad port filter: %q", text)
	}
}

// TestMCPPanicBecomesInternalError: a panicking handler answers -32603 and
// the session keeps serving.
func TestMCPPanicBecomesInternalError(t *testing.T) {
	old := activePorts
	t.Cleanup(func() { activePorts = old })
	activePorts = func() ([]PortItem, error) { panic("boom") }
	r := mcpRun(t, mcpServer{},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_ports","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(r) != 2 {
		t.Fatalf("replies = %v", r)
	}
	e, _ := r[0]["error"].(map[string]any)
	if e == nil || e["code"].(float64) != -32603 || r[0]["id"].(float64) != 1 {
		t.Errorf("panic reply = %v", r[0])
	}
	if r[1]["result"] == nil {
		t.Error("server stopped after a panic")
	}
}

// TestMCPInvalidRequests: valid JSON that is not a request object (a batch,
// a number, a missing method) is -32600 Invalid Request, not a parse error.
func TestMCPInvalidRequests(t *testing.T) {
	r := mcpRun(t, mcpServer{},
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`42`,
		`{"jsonrpc":"2.0","id":3}`,
		`{not json`)
	want := []float64{-32600, -32600, -32600, -32700}
	if len(r) != len(want) {
		t.Fatalf("replies = %v", r)
	}
	for i, w := range want {
		e, _ := r[i]["error"].(map[string]any)
		if e == nil || e["code"].(float64) != w {
			t.Errorf("reply %d = %v, want code %v", i, r[i], w)
		}
	}
	if r[2]["id"].(float64) != 3 {
		t.Errorf("missing-method reply must echo the id: %v", r[2])
	}
}

// TestMCPDestructiveArgsAreStrict: a misspelled dry_run ("dryRun") was
// silently ignored, so with --allow-kill the "preview" really stopped the
// listener. Unknown arguments, empty names and invalid ports are refused,
// and ports are normalised so "03000" means 3000.
func TestMCPDestructiveArgsAreStrict(t *testing.T) {
	sigs := stubSignals(t)
	stubActivePorts(t, []PortItem{{Port: "3000", PID: "99999999", Process: "api", Source: "host"}})
	s := mcpServer{allowKill: true}
	for args, want := range map[string]string{
		`{"ports":["3000"],"dryRun":true}`:  `unknown argument "dryRun"`,
		`{"names":[""],"dry_run":true}`:     "empty name",
		`{"ports":["abc"],"dry_run":true}`:  "bad port",
		`{"ports":["3000"],"range":"1-2"}`:  `unknown argument "range"`,
		`{"ports":["3000"],"signal":"HUP"}`: "",
	} {
		text, isErr := callText(t, s, "stop_port", args)
		if want == "" {
			continue
		}
		if !isErr || !strings.Contains(text, want) {
			t.Errorf("%s: %q, want error %q", args, text, want)
		}
	}
	if len(*sigs) != 1 {
		t.Fatalf("only the explicit real stop may signal, got %v", *sigs)
	}
	for _, tool := range []string{"restart_port", "stop_port"} {
		if text, isErr := callText(t, s, tool, `{"names":[""],"dry_run":true}`); !isErr || !strings.Contains(text, "empty name") {
			t.Errorf("%s empty name: %q", tool, text)
		}
		if text, isErr := callText(t, s, tool, `{"ports":["3000"],"dry-run":true}`); !isErr || !strings.Contains(text, "unknown argument") {
			t.Errorf("%s misspelled dry run: %q", tool, text)
		}
	}
	text, isErr := callText(t, s, "stop_port", `{"ports":["03000"],"dry_run":true}`)
	if isErr || !strings.Contains(text, "would-stop") || strings.Contains(text, "no_listener") {
		t.Errorf("03000 must match port 3000: %q", text)
	}
	if text, _ := callText(t, s, "list_ports", `{"port":"03000"}`); strings.Count(text, `"Port"`) != 1 {
		t.Errorf("list_ports 03000: %q", text)
	}
	if text, isErr := callText(t, s, "list_ports", `{"port":"x"}`); !isErr || !strings.Contains(text, "bad port") {
		t.Errorf("list_ports bad port: %q", text)
	}
}

// TestMCPNonObjectArguments: arguments that are not an object are refused
// by the typed decode; null means "no arguments".
func TestMCPNonObjectArguments(t *testing.T) {
	if text, isErr := callText(t, mcpServer{}, "stop_port", `[1]`); !isErr || !strings.Contains(text, "invalid arguments") {
		t.Errorf("array arguments: %q isErr=%v", text, isErr)
	}
	if text, isErr := callText(t, mcpServer{}, "port_history", `null`); isErr {
		t.Errorf("null arguments: %q", text)
	}
}
