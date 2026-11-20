package main

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestClientConns: the client side of a local connection to port P is the
// socket whose remote port is P and whose local port is the remote port of
// one of the server's established sockets. Server sockets (local port P)
// and connections to some other host's port P are not clients.
func TestClientConns(t *testing.T) {
	conns := []Conn{
		{LocalPort: 8080, RemotePort: 40001, State: "01", Inode: "srv1"},
		{LocalPort: 40001, RemotePort: 8080, State: "01", Inode: "cli1"},
		{LocalPort: 8080, RemotePort: 40002, State: "01", Inode: "srv2"}, // remote client
		{LocalPort: 50000, RemotePort: 8080, State: "01", Inode: "elsewhere"},
		{LocalPort: 40003, RemotePort: 8080, State: "06", Inode: "closing"},
		{LocalPort: 8080, RemotePort: 0, State: "0A", Inode: "listen"},
	}
	est := countEstablished(conns, 8080)
	got := clientConns(conns, est, 8080)
	if len(got) != 1 || got[0].Inode != "cli1" {
		t.Fatalf("clients = %+v, want only cli1", got)
	}
}

// TestComputeBlastNamesTheClientNotTheServer: with a real python3 client
// connected to a listener in this process, the clients list holds python3,
// not the server (the test binary), which it used to list.
func TestComputeBlastNamesTheClientNotTheServer(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	c := exec.Command(py, "-c", "import socket,sys,time\ns=socket.create_connection(('127.0.0.1',int(sys.argv[1])))\ntime.sleep(20)", port)
	if err := c.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	select {
	case sc := <-accepted:
		defer sc.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("client never connected")
	}
	self, _ := os.ReadFile("/proc/self/comm")
	b := ComputeBlast(PortItem{Port: port, PID: itoaSimple(os.Getpid())})
	if len(b.Clients) != 1 || !strings.HasPrefix(b.Clients[0], "python") {
		t.Fatalf("clients = %v, want [python3] (server is %q)", b.Clients, strings.TrimSpace(string(self)))
	}
}
