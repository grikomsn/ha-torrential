//go:build linux

package main

import (
	"net/netip"
	"testing"
)

func testResponder() *responder {
	return &responder{
		name:   "couch-potato.local",
		labels: []string{"couch-potato", "local"},
		addrs: map[string][]netip.Addr{
			"end0":  {netip.MustParseAddr("192.168.1.26"), netip.MustParseAddr("2001:db8::26")},
			"wlan0": {netip.MustParseAddr("192.168.1.27")},
		},
	}
}

func TestAnAnnouncementCarriesTheInterfacesAddressesAndSaysThereIsNothingElse(t *testing.T) {
	msg, ok := parse(testResponder().message("end0", 0, nil, ttl, true))
	if !ok || !msg.response || len(msg.questions) != 0 || len(msg.records) != 3 {
		t.Fatalf("got %+v", msg)
	}
	a, aaaa, nsec := msg.records[0], msg.records[1], msg.records[2]
	if a.kind != typeA || netip.AddrFrom4([4]byte(a.data)).String() != "192.168.1.26" || a.ttl != ttl {
		t.Errorf("A record: %+v", a)
	}
	if aaaa.kind != typeAAAA || netip.AddrFrom16([16]byte(aaaa.data)).String() != "2001:db8::26" {
		t.Errorf("AAAA record: %+v", aaaa)
	}
	for _, rec := range msg.records {
		if rec.name != "couch-potato.local" {
			t.Errorf("record for %q", rec.name)
		}
	}
	// The next name, then window 0 with four bytes: A is bit 1, AAAA is bit 28.
	bitmap := nsec.data[len(nsec.data)-6:]
	if nsec.kind != typeNSEC || string(bitmap) != string([]byte{0, 4, 0x40, 0, 0, 0x08}) {
		t.Errorf("NSEC record: %+v", nsec)
	}
}

func TestAnInterfaceWithoutIPv6SaysSoInAShorterBitmap(t *testing.T) {
	msg, _ := parse(testResponder().message("wlan0", 0, nil, ttl, true))
	if len(msg.records) != 2 {
		t.Fatalf("got %+v", msg)
	}
	nsec := msg.records[1]
	if string(nsec.data[len(nsec.data)-3:]) != string([]byte{0, 1, 0x40}) {
		t.Errorf("NSEC record: %+v", nsec)
	}
}

func TestAPlainDNSClientGetsItsQuestionAndIDBack(t *testing.T) {
	q := question{name: "couch-potato.local", kind: typeA, class: classIN}
	msg, _ := parse(testResponder().message("end0", 0x1234, &q, 10, false))
	if msg.id != 0x1234 || len(msg.questions) != 1 || msg.questions[0] != q || msg.records[0].ttl != 10 {
		t.Errorf("got %+v", msg)
	}
}

func TestANameIsReadThroughACompressionPointerAndLowercased(t *testing.T) {
	// "local" at 0, then "Couch-Potato" followed by a pointer to it.
	data := append([]byte{5, 'l', 'o', 'c', 'a', 'l', 0}, 12)
	data = append(data, "Couch-Potato"...)
	data = append(data, 0xc0, 0x00, 0xff)
	name, next, ok := readName(data, 7)
	if !ok || name != "couch-potato.local" || data[next] != 0xff {
		t.Errorf("got %q, %d, %t", name, next, ok)
	}
	// A pointer that points at itself ends, and is refused.
	if _, _, ok := readName([]byte{0xc0, 0x00}, 0); ok {
		t.Error("a pointer loop was read as a name")
	}
}

func TestOnlyALabelABrowserCanTypeIsAName(t *testing.T) {
	for name, want := range map[string]bool{"couch-potato": true, "tv2": true, "": false, "-tv": false, "tv-": false, "tv.local": false, "tv room": false} {
		if validLabel(name) != want {
			t.Errorf("validLabel(%q) = %t", name, !want)
		}
	}
}

func TestAnotherMachinesAddressForTheNameIsAConflictAndOurOwnIsNot(t *testing.T) {
	r := testResponder()
	theirs := testResponder()
	theirs.addrs = map[string][]netip.Addr{"end0": {netip.MustParseAddr("192.168.1.99")}}
	from := &listener{iface: "end0"}
	other := packet{from: from, src: netip.MustParseAddrPort("192.168.1.99:5353"), data: theirs.message("end0", 0, nil, ttl, true)}
	if _, conflict := r.conflict(other, mustParse(t, other.data)); !conflict {
		t.Error("another machine's answer was not a conflict")
	}
	own := packet{from: from, src: netip.MustParseAddrPort("192.168.1.26:5353"), data: r.message("end0", 0, nil, ttl, true)}
	if _, conflict := r.conflict(own, mustParse(t, own.data)); conflict {
		t.Error("this machine's own answer was a conflict")
	}
	// A goodbye from the other machine gives the name up.
	bye := packet{from: from, src: other.src, data: theirs.message("end0", 0, nil, 0, true)}
	if _, conflict := r.conflict(bye, mustParse(t, bye.data)); conflict {
		t.Error("a goodbye was a conflict")
	}
}

func mustParse(t *testing.T, data []byte) message {
	t.Helper()
	msg, ok := parse(data)
	if !ok {
		t.Fatal("unreadable message")
	}
	return msg
}
