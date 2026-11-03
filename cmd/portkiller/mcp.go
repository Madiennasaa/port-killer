package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// mcpVersions are the MCP protocol revisions this server speaks, newest
// first. The tool surface used here is identical across all of them.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// mcpInstructions is sent at initialize so the client model knows the limits.
const mcpInstructions = "portkiller inspects and stops TCP/UDP listeners on this Linux machine. " +
	"Use list_ports to see who holds a port, free_port to pick an unused one, " +
	"stop_port or restart_port with dry_run first. Real stops only work when the server was started " +
	"with --allow-kill. Without root, listeners of other users show no PID."

type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// mcpServer holds what a session may do; allowKill gates real stops.
type mcpServer struct {
	allowKill bool
}

// cliMCP serves MCP on stdin/stdout until the client closes stdin.
func cliMCP(args []string) {
	s := mcpServer{allowKill: hasFlag(args, "--allow-kill")}
	if err := s.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "mcp:", err)
		exitProcess(1)
	}
}

// serve reads newline-delimited JSON-RPC messages and answers each request.
// Notifications (no id) get no reply, as JSON-RPC requires.
func (s mcpServer) serve(in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		resp, ok := s.handle([]byte(line))
		if !ok {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// handle answers one message; ok=false means it was a notification.
func (s mcpServer) handle(line []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}, true
	}
	if len(req.ID) == 0 {
		return rpcResponse{}, false
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = initializeResult(req.Params)
	case "ping":
		resp.Result = struct{}{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "invalid params: " + err.Error()}
			break
		}
		res, err := s.callTool(p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			resp.Error = &rpcError{-32602, err.Error()}
			break
		}
		resp.Result = toolResult(res, err)
	default:
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp, true
}

// initializeResult echoes the client's protocol version when supported and
// otherwise offers the newest one, as the MCP handshake specifies.
func initializeResult(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &p)
	v := mcpVersions[0]
	for _, known := range mcpVersions {
		if known == p.ProtocolVersion {
			v = known
		}
	}
	return map[string]any{
		"protocolVersion": v,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]string{"name": "portkiller", "version": version},
		"instructions":    mcpInstructions,
	}
}

// toolResult wraps a tool's value as MCP text content; a tool error is
// reported in-band (isError) so the model can read and react to it.
func toolResult(v any, err error) map[string]any {
	if err != nil {
		return map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}}
}

// schema builds a JSON Schema object with the given properties.
func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	strProp  = map[string]any{"type": "string"}
	strsProp = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	boolProp = map[string]any{"type": "boolean"}
	intProp  = map[string]any{"type": "integer"}
)

func mcpTools() []map[string]any {
	ro := map[string]any{"readOnlyHint": true}
	return []map[string]any{
		{
			"name":        "list_ports",
			"description": "List listening sockets with process, PID, project, git branch and container info. Filters are optional; process matches a substring.",
			"inputSchema": schema(map[string]any{"port": strProp, "process": strProp, "proto": map[string]any{"type": "string", "enum": []string{"tcp", "udp"}}}),
			"annotations": ro,
		},
		{
			"name":        "free_port",
			"description": "Return the first TCP port that can be bound in a range such as \"3000-3010\".",
			"inputSchema": schema(map[string]any{"range": strProp}, "range"),
			"annotations": ro,
		},
		{
			"name":        "port_history",
			"description": "Recent appear/vanished events recorded by portkiller, newest first.",
			"inputSchema": schema(map[string]any{"port": strProp, "limit": intProp}),
			"annotations": ro,
		},
		{
			"name": "stop_port",
			"description": "Stop the listeners on the given ports and/or with the given process or container names. " +
				"Defaults to SIGTERM then SIGKILL; containers use docker/podman stop and systemd services systemctl. " +
				"dry_run previews without stopping; real stops need the server to run with --allow-kill. " +
				"The result flags listeners a process manager (pm2, supervisord...) may restart.",
			"inputSchema": schema(map[string]any{
				"ports": strsProp, "names": strsProp, "force": boolProp,
				"signal": strProp, "dry_run": boolProp,
			}),
			"annotations": map[string]any{"destructiveHint": true},
		},
		{
			"name": "restart_port",
			"description": "Restart the listeners on the given ports and/or names: stop them, wait until the port is free, then run " +
				"the same command line again in the same directory and environment (systemd units via systemctl, containers via start). " +
				"Refuses, stopping nothing, when a target cannot be relaunched or a process manager owns it. Needs --allow-kill unless dry_run.",
			"inputSchema": schema(map[string]any{
				"ports": strsProp, "names": strsProp, "force": boolProp, "dry_run": boolProp,
			}),
			"annotations": map[string]any{"destructiveHint": true},
		},
	}
}

var errUnknownTool = errors.New("unknown tool")

// callTool decodes arguments and runs one tool.
func (s mcpServer) callTool(name string, raw json.RawMessage) (any, error) {
	var a struct {
		Port    string   `json:"port"`
		Process string   `json:"process"`
		Proto   string   `json:"proto"`
		Range   string   `json:"range"`
		Limit   int      `json:"limit"`
		Ports   []string `json:"ports"`
		Names   []string `json:"names"`
		Force   bool     `json:"force"`
		Signal  string   `json:"signal"`
		DryRun  bool     `json:"dry_run"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	switch name {
	case "list_ports":
		return toolListPorts(a.Port, a.Process, a.Proto)
	case "free_port":
		return toolFreePort(a.Range)
	case "port_history":
		return map[string]any{"events": historyEvents(loadEvents(eventsPath()), a.Port, a.Limit)}, nil
	case "stop_port":
		return s.toolStopPort(a.Ports, a.Names, a.Force, a.Signal, a.DryRun)
	case "restart_port":
		return s.toolRestartPort(a.Ports, a.Names, a.Force, a.DryRun)
	}
	return nil, fmt.Errorf("%w %q", errUnknownTool, name)
}

func toolListPorts(port, process, proto string) (any, error) {
	items, err := activePorts()
	out := []PortItem{}
	for _, it := range items {
		if (port == "" || it.Port == port) &&
			(proto == "" || strings.TrimSuffix(it.Proto, "6") == proto) &&
			(process == "" || strings.Contains(strings.ToLower(it.Process), strings.ToLower(process))) {
			out = append(out, it)
		}
	}
	res := map[string]any{"listeners": out}
	if err != nil {
		res["warning"] = err.Error()
	}
	return res, nil
}

func toolFreePort(spec string) (any, error) {
	start, end, err := parsePortRange(spec)
	if err != nil {
		return nil, err
	}
	port, ok := firstFreePort(start, end)
	if !ok {
		return nil, fmt.Errorf("no free port in %s", spec)
	}
	return map[string]int{"port": port}, nil
}

// stopOutcome is one listener's line in a stop_port result.
type stopOutcome struct {
	Process    string `json:"process"`
	PID        string `json:"pid"`
	Port       string `json:"port"`
	Via        string `json:"via"`
	Status     string `json:"status"` // would-stop | stopped | failed
	Error      string `json:"error,omitempty"`
	Supervisor string `json:"supervisor,omitempty"`
}

// toolStopPort mirrors --kill without printing: stdout is the protocol.
func (s mcpServer) toolStopPort(ports, names []string, force bool, signal string, dryRun bool) (any, error) {
	if len(ports) == 0 && len(names) == 0 {
		return nil, errors.New("give ports and/or names")
	}
	for _, p := range ports {
		if _, err := parsePort(p); err != nil {
			return nil, err
		}
	}
	var opts killOpts
	if signal != "" {
		if force {
			return nil, errors.New("signal and force cannot be combined")
		}
		sig, name, err := parseSignal(signal)
		if err != nil {
			return nil, err
		}
		opts.sig, opts.sigName = sig, name
	}
	if !dryRun && !s.allowKill {
		return nil, errors.New("stopping is disabled: restart the server as `portkiller --mcp --allow-kill`, or pass dry_run")
	}
	items, _ := activePorts()
	sel := selectTargets(ports, names, items)
	stop := opts.stopFunc()
	seen := map[string]bool{}
	results := []stopOutcome{}
	for _, it := range sel {
		if seen[it.PID] {
			continue
		}
		seen[it.PID] = true
		o := stopOutcome{Process: it.Process, PID: it.PID, Port: it.Port, Via: opts.verb(force), Status: "would-stop"}
		if it.IsContainer() {
			o.Via = runtimeOf(it.Source) + " stop"
			if opts.sig != 0 {
				o.Via = runtimeOf(it.Source) + " kill --signal " + opts.sigName
			}
		} else if pid, err := strconv.Atoi(it.PID); err == nil {
			if sup, ok := supervisorOf(pid); ok {
				o.Supervisor = sup.Name + " may restart it; stop it with: " + sup.Hint
			}
		}
		if !dryRun {
			o.Status = "stopped"
			if err := stop(it, force); err != nil {
				o.Status, o.Error = "failed", err.Error()
			}
		}
		results = append(results, o)
	}
	res := map[string]any{"results": results}
	missing := append(missingPorts(ports, items), missingKeys(names, items, containerName)...)
	if len(missing) > 0 {
		res["no_listener"] = missing
	}
	return res, nil
}

// toolRestartPort mirrors --restart without printing.
func (s mcpServer) toolRestartPort(ports, names []string, force, dryRun bool) (any, error) {
	if len(ports) == 0 && len(names) == 0 {
		return nil, errors.New("give ports and/or names")
	}
	for _, p := range ports {
		if _, err := parsePort(p); err != nil {
			return nil, err
		}
	}
	if !dryRun && !s.allowKill {
		return nil, errors.New("restarting is disabled: restart the server as `portkiller --mcp --allow-kill`, or pass dry_run")
	}
	items, _ := activePorts()
	results, err := restartTargets(selectTargets(ports, names, items), force, stopItem, defaultWait, dryRun)
	if err != nil {
		return nil, err
	}
	res := map[string]any{"results": results}
	missing := append(missingPorts(ports, items), missingKeys(names, items, containerName)...)
	if len(missing) > 0 {
		res["no_listener"] = missing
	}
	return res, nil
}
