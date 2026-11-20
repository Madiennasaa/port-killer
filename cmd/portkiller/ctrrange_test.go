package main

import "testing"

// TestParseContainerPSPortRange: docker/podman print a published range as
// "0.0.0.0:8000-8002->8000-8002/tcp"; each host port must become its own
// row (a "8000-8002" port matches nothing and cannot be filtered or freed).
func TestParseContainerPSPortRange(t *testing.T) {
	out := "abc|web|0.0.0.0:8000-8002->8000-8002/tcp, [::]:9000->90/udp|Up 1 hour\n"
	got := parseContainerPS(out, "docker")
	want := []string{"tcp/8000", "tcp/8001", "tcp/8002", "udp/9000"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, it := range got {
		if it.Proto+"/"+it.Port != want[i] {
			t.Fatalf("row %d = %s/%s, want %s", i, it.Proto, it.Port, want[i])
		}
	}
}

// TestParseContainerPSBadRanges: reversed, non-numeric, out-of-range and
// oversized ranges are skipped rather than producing junk or huge tables.
func TestParseContainerPSBadRanges(t *testing.T) {
	for _, p := range []string{
		"0.0.0.0:9-1->9-1/tcp",
		"0.0.0.0:a-3->1-3/tcp",
		"0.0.0.0:1-b->1-3/tcp",
		"0.0.0.0:0-2->0-2/tcp",
		"0.0.0.0:65535-65536->1-2/tcp",
		"0.0.0.0:1-60000->1-60000/tcp",
		"0.0.0.0:x->80/tcp",
	} {
		if got := parseContainerPS("id|n|"+p+"|Up", "docker"); len(got) != 0 {
			t.Errorf("%s -> %+v, want none", p, got)
		}
	}
}

func TestExpandPortRange(t *testing.T) {
	if got := expandPortRange("80"); len(got) != 1 || got[0] != "80" {
		t.Fatalf("got %v", got)
	}
	if got := expandPortRange("1-1"); len(got) != 1 || got[0] != "1" {
		t.Fatalf("got %v", got)
	}
}
