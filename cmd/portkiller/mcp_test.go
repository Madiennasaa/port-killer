package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// mcpRun feeds lines to a server and decodes every reply.
func mcpRun(t *testing.T, s mcpServer, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	var replies []map[string]any
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r map[string]any
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		replies = append(replies, r)
	}
	return replies
}

// callText runs one tools/call and returns its text and isError flag.
func callText(t *testing.T, s mcpServer, name, args string) (string, bool) {
	t.Helper()
	r := mcpRun(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	res, ok := r[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no result: %v", name, r[0])
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	isErr, _ := res["isError"].(bool)
	return text, isErr
}

func TestMCPProtocol(t *testing.T) {
	r := mcpRun(t, mcpServer{},
		``,
		`{not json`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"nope"}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"rm_rf"}}`,
	)
	if len(r) != 8 {
		t.Fatalf("blank lines and notifications must get no reply: %d replies", len(r))
	}
	code := func(i int) float64 { return r[i]["error"].(map[string]any)["code"].(float64) }
	if code(0) != -32700 || r[0]["id"] != nil {
		t.Errorf("parse error: %v", r[0])
	}
	if v := r[1]["result"].(map[string]any)["protocolVersion"]; v != "2025-03-26" {
		t.Errorf("known version must be echoed: %v", v)
	}
	if v := r[2]["result"].(map[string]any)["protocolVersion"]; v != mcpVersions[0] {
		t.Errorf("unknown version must get the newest: %v", v)
	}
	if _, ok := r[3]["result"].(map[string]any); !ok {
		t.Errorf("ping: %v", r[3])
	}
	if tools := r[4]["result"].(map[string]any)["tools"].([]any); len(tools) != 5 {
		t.Errorf("tools: %v", tools)
	}
	if code(5) != -32601 || code(6) != -32602 || code(7) != -32602 {
		t.Errorf("errors: %v %v %v", r[5], r[6], r[7])
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestMCPServeIOErrors(t *testing.T) {
	if err := (mcpServer{}).serve(strings.NewReader(`{"id":1,"method":"ping"}`+"\n"), failWriter{}); err == nil {
		t.Error("write failure must surface")
	}
	if err := (mcpServer{}).serve(failReader{}, &bytes.Buffer{}); err == nil {
		t.Error("read failure must surface")
	}
}

func TestMCPListPorts(t *testing.T) {
	old := activePorts
	t.Cleanup(func() { activePorts = old })
	activePorts = func() ([]PortItem, error) {
		return []PortItem{
			{Proto: "tcp6", Port: "3000", Process: "Node"},
			{Proto: "udp", Port: "3000", Process: "node"},
			{Proto: "tcp", Port: "5432", Process: "postgres"},
		}, errors.New("ss partial")
	}
	text, isErr := callText(t, mcpServer{}, "list_ports", `{"port":"3000","proto":"tcp","process":"node"}`)
	if isErr || strings.Count(text, `"Port"`) != 1 || !strings.Contains(text, "tcp6") || !strings.Contains(text, "ss partial") {
		t.Errorf("filtered list: %s", text)
	}
	if text, _ := callText(t, mcpServer{}, "list_ports", `{}`); strings.Count(text, `"Port"`) != 3 {
		t.Errorf("unfiltered list: %s", text)
	}
	if text, isErr := callText(t, mcpServer{}, "list_ports", `[1]`); !isErr || !strings.Contains(text, "invalid arguments") {
		t.Errorf("bad args: %s", text)
	}
}

func TestMCPFreePortAndHistory(t *testing.T) {
	old := portFree
	t.Cleanup(func() { portFree = old })
	portFree = func(p int) bool { return p == 3002 }
	if text, isErr := callText(t, mcpServer{}, "free_port", `{"range":"3000-3005"}`); isErr || !strings.Contains(text, "3002") {
		t.Errorf("free: %s", text)
	}
	if _, isErr := callText(t, mcpServer{}, "free_port", `{"range":"3000-3001"}`); !isErr {
		t.Error("full range must be an error")
	}
	if _, isErr := callText(t, mcpServer{}, "free_port", `{"range":"x"}`); !isErr {
		t.Error("bad range must be an error")
	}
	withTempHome(t)
	if text, isErr := callText(t, mcpServer{}, "port_history", `{"limit":5}`); isErr || !strings.Contains(text, "events") {
		t.Errorf("history: %s", text)
	}
}

func TestMCPStopPortValidation(t *testing.T) {
	for args, want := range map[string]string{
		`{}`:                  "give ports",
		`{"ports":["99999"]}`: "out of range",
		`{"ports":["80"],"signal":"HUP","force":true}`: "cannot be combined",
		`{"ports":["80"],"signal":"BOGUS"}`:            "unknown signal",
		`{"ports":["80"]}`:                             "--allow-kill",
	} {
		if text, isErr := callText(t, mcpServer{}, "stop_port", args); !isErr || !strings.Contains(text, want) {
			t.Errorf("%s: %q", args, text)
		}
	}
}

func TestMCPStopPortDryRunAndReal(t *testing.T) {
	stubProcTree(t, map[string]string{"/proc/42/environ": "pm_id=2\x00"})
	sigs := stubSignals(t)
	stubActivePorts(t, []PortItem{
		{Port: "3000", PID: "42", Process: "api"},
		{Port: "3000", PID: "42", Process: "api", Proto: "tcp6"},
		{Port: "8080", PID: "abc", Process: "podman:web", Source: "podman"},
		{Port: "9000", PID: "x", Process: "weird"},
	})
	text, isErr := callText(t, mcpServer{}, "stop_port", `{"ports":["3000","4000"],"names":["web","ghost"],"signal":"HUP","dry_run":true}`)
	if isErr || strings.Count(text, "would-stop") != 2 || !strings.Contains(text, "pm2 stop 2") ||
		!strings.Contains(text, "podman kill --signal SIGHUP") || !strings.Contains(text, `"4000"`) || !strings.Contains(text, `"ghost"`) {
		t.Errorf("dry run: %s", text)
	}
	if len(*sigs) != 0 {
		t.Fatal("dry run must not signal")
	}
	if text, _ := callText(t, mcpServer{}, "stop_port", `{"names":["web"],"dry_run":true}`); !strings.Contains(text, `"podman stop"`) {
		t.Errorf("container via: %s", text)
	}
	text, isErr = callText(t, mcpServer{allowKill: true}, "stop_port", `{"ports":["3000","9000"],"signal":"TERM"}`)
	if isErr || !strings.Contains(text, `"stopped"`) || !strings.Contains(text, `"failed"`) || len(*sigs) != 1 || (*sigs)[0] != syscall.SIGTERM {
		t.Errorf("real stop: %s %v", text, *sigs)
	}
	if strings.Contains(text, "no_listener") {
		t.Errorf("every requested port had a listener: %s", text)
	}
}

func TestCLIMCPStdio(t *testing.T) {
	oldIn, oldOut := os.Stdin, os.Stdout
	t.Cleanup(func() { os.Stdin, os.Stdout = oldIn, oldOut })
	in, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = in.WriteString(`{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n")
	_, _ = in.Seek(0, 0)
	os.Stdin = in
	code := stubExit(t)
	out := captureStdout(t, func() {
		if !runCLI([]string{"--mcp", "--allow-kill"}) {
			t.Error("--mcp must be handled")
		}
	})
	if !strings.Contains(out, `"id":9`) || *code != 0 || cliNeedsRoot([]string{"--mcp", "--kill", "1"}) {
		t.Errorf("out %q code %d", out, *code)
	}
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = dir // reading a directory fails
	captureStderr(t, func() { cliMCP(nil) })
	if *code != 1 {
		t.Errorf("read failure: exit %d, want 1", *code)
	}
}
