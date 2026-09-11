package main

import (
	"encoding/binary"
	"net"
	"syscall"
	"testing"
	"time"
)

// RFC 1071 section 3 worked example: words 0001 f203 f4f5 f6f7 sum to ddf2.
func TestICMPChecksumRFC1071(t *testing.T) {
	if got := icmpChecksum([]byte{0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7}); got != ^uint16(0xddf2) {
		t.Fatalf("checksum %#04x, want %#04x", got, ^uint16(0xddf2))
	}
	if got := icmpChecksum([]byte{0xff}); got != ^uint16(0xff00) {
		t.Fatalf("odd length: %#04x", got)
	}
}

func TestBuildEcho(t *testing.T) {
	nonce := [4]byte{1, 2, 3, 4}
	p := buildEcho(0xbeef, 7, nonce)
	if p[0] != 8 || p[1] != 0 {
		t.Fatalf("type/code: %d/%d", p[0], p[1])
	}
	if binary.BigEndian.Uint16(p[4:6]) != 0xbeef || binary.BigEndian.Uint16(p[6:8]) != 7 {
		t.Fatal("id/seq not encoded big-endian")
	}
	if string(p[8:8+len(icmpMagic)]) != icmpMagic || string(p[8+len(icmpMagic):]) != string(nonce[:]) {
		t.Fatal("payload is not magic+nonce")
	}
	// a packet carrying its own checksum sums to zero
	if icmpChecksum(p) != 0 {
		t.Fatal("checksum does not verify")
	}
}

func TestResolveIPv4(t *testing.T) {
	ip, err := resolveIPv4("127.0.0.1")
	if err != nil || ip != [4]byte{127, 0, 0, 1} {
		t.Fatalf("literal: %v %v", ip, err)
	}
	if _, err := resolveIPv4("::1"); err == nil {
		t.Fatal("IPv6-only host must be rejected")
	}
}

func TestTCPRoundOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	r := tcpRound(ln.Addr().String(), 5, time.Millisecond, 500*time.Millisecond)
	if r.S != 5 || r.R != 5 || len(r.MS) != 5 {
		t.Fatalf("open port: %+v", r)
	}
	for _, v := range r.MS {
		if v < 0 || v > 500 {
			t.Fatalf("implausible connect time %vms", v)
		}
	}
}

func TestTCPRoundClosedPort(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // refused from now on
	r := tcpRound(addr, 3, time.Millisecond, 200*time.Millisecond)
	if r.S != 3 || r.R != 0 || len(r.MS) != 0 {
		t.Fatalf("closed port: %+v", r)
	}
}

// Needs ICMP permission (ping_group_range or CAP_NET_RAW); skipped without it.
func TestICMPRoundLoopback(t *testing.T) {
	fd, _, err := icmpSocket()
	if err != nil {
		t.Skipf("no ICMP permission here: %v", err)
	}
	syscall.Close(fd)
	r, err := icmpRound("127.0.0.1", 3, 10*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.S != 3 || r.R != 3 || len(r.MS) != 3 {
		t.Fatalf("loopback ping: %+v", r)
	}
}
