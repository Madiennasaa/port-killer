package main

import (
	"reflect"
	"testing"
)

const sampleTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0 100 0 0 10 0
   1: 0100007F:1F40 0100007F:A000 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0 100 0 0 10 0
   2: 0100007F:1F40 0100007F:A001 01 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0 100 0 0 10 0
`

const sampleTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:1F40 00000000000000000000000001000000:C000 01 00000000:00000000 00:00000000 00000000  1000        0 44444 1 0 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	c := parseProcNetTCP([]byte(sampleTCP))
	if len(c) != 3 || c[0].LocalPort != 8000 || c[0].State != "0A" || c[1].RemotePort != 0xA000 {
		t.Fatalf("got %+v", c)
	}
	c6 := parseProcNetTCP([]byte(sampleTCP6))
	if len(c6) != 1 || c6[0].LocalPort != 8000 || c6[0].Inode != "44444" {
		t.Fatalf("ipv6 got %+v", c6)
	}
	if len(parseProcNetTCP(nil)) != 0 || len(parseProcNetTCP([]byte("hdr\nbad line"))) != 0 {
		t.Fatal("input rusak harus kosong")
	}
}

func TestCountEstablished(t *testing.T) {
	c := parseProcNetTCP([]byte(sampleTCP))
	if n := len(countEstablished(c, 8000)); n != 2 {
		t.Fatalf("got %d", n)
	}
	if n := len(countEstablished(c, 9999)); n != 0 {
		t.Fatalf("got %d", n)
	}
}

func TestCountChildren(t *testing.T) {
	pp := map[int]int{10: 1, 11: 10, 12: 10, 13: 11}
	if n := countChildren(pp, 10); n != 2 {
		t.Fatalf("got %d", n)
	}
	if n := countChildren(pp, 99); n != 0 {
		t.Fatalf("got %d", n)
	}
}

func TestSummarizeClients(t *testing.T) {
	got := summarizeClients([]string{"php", "", "chromium", "php"})
	if !reflect.DeepEqual(got, []string{"chromium", "php"}) {
		t.Fatalf("got %v", got)
	}
}

func TestBlastText(t *testing.T) {
	if blastText(Blast{}, true) != "calculating impact..." {
		t.Fatal("loading")
	}
	if blastText(Blast{}, false) != "no impact detected" {
		t.Fatal("zero")
	}
	got := blastText(Blast{Conns: 3, Children: 2, Clients: []string{"php"}}, false)
	if got != "dropping 3 connections (php), 2 child processes" {
		t.Fatalf("got %q", got)
	}
}
