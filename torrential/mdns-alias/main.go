//go:build linux

// mdns-alias answers for one extra .local name over mDNS (RFC 6762), with the
// addresses of the machine it runs on. It is a responder for that name only:
// no services, no browsing, no reverse lookups.
//
// The app runs on the host's network, where something else already answers
// mDNS for the host's own name (systemd-resolved on Home Assistant OS) and
// others listen too (Home Assistant's zeroconf, the Supervisor's mDNS
// repeater). To stay out of their way it
//
//   - binds each socket to the mDNS group address, not to 0.0.0.0, so it
//     never takes a unicast packet meant for one of them;
//   - opens one socket per home network interface, the ones with a default
//     route, and hears only what arrives there (IP_MULTICAST_ALL off). The
//     Docker and Supervisor bridges and Tailscale are left alone, so their
//     addresses are never announced;
//   - answers nothing while it checks that no other machine has the name, and
//     exits if one does.
//
// Usage: mdns-alias NAME   (a single DNS label, announced as NAME.local)
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	port     = 5353
	ttl      = 120 // seconds, what RFC 6762 recommends for host names
	classIN  = 1
	flush    = 0x8000 // cache-flush bit on a record, unicast-response bit on a question
	typeA    = 1
	typeAAAA = 28
	typeNSEC = 47
	typeANY  = 255

	probes        = 3
	probeInterval = 250 * time.Millisecond
	rescan        = 5 * time.Second
	// A record is not multicast more than once a second.
	quiet = time.Second
)

// Linux values that package syscall has no names for. It names SO_REUSEPORT
// on arm64 but not on amd64; the value is the same on both.
const (
	soReusePort      = 15
	ipMulticastAll   = 49
	ipv6MulticastAll = 29
)

var (
	group4 = netip.MustParseAddr("224.0.0.251")
	group6 = netip.MustParseAddr("ff02::fb")
)

// Set MDNS_ALIAS_DEBUG to log every packet that arrives.
var debug = os.Getenv("MDNS_ALIAS_DEBUG") != ""

func logf(format string, args ...any) {
	fmt.Printf("[mdns] "+format+"\n", args...)
}

// listener is the socket for one address family on one interface.
type listener struct {
	iface string
	v6    bool
	conn  *net.UDPConn
	group netip.AddrPort
	// When the records last went out by multicast here.
	sent time.Time
	// Why the last send failed, if it did.
	failed string
}

type packet struct {
	from *listener
	src  netip.AddrPort
	data []byte
}

type responder struct {
	name    string   // lowercase, with .local
	labels  []string // the name's labels
	addrs   map[string][]netip.Addr
	socks   map[string]*listener // by interface and family
	packets chan packet
	probing bool
	warned  time.Time
}

func main() {
	if len(os.Args) != 2 || !validLabel(os.Args[1]) {
		fmt.Fprintln(os.Stderr, "usage: mdns-alias NAME, where NAME is one DNS label such as couch-potato")
		os.Exit(64)
	}
	label := strings.ToLower(os.Args[1])
	r := &responder{
		name:    label + ".local",
		labels:  []string{label, "local"},
		addrs:   map[string][]netip.Addr{},
		socks:   map[string]*listener{},
		packets: make(chan packet, 64),
		probing: true,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	r.scan()
	if len(r.socks) == 0 {
		logf("no home network interface yet, waiting for one")
	}
	probe := time.NewTicker(probeInterval)
	defer probe.Stop()
	probesLeft := probes
	// Announced twice, a second apart, and again whenever the addresses change.
	announce := time.NewTimer(time.Hour)
	announce.Stop()
	announcesLeft := 0
	scan := time.NewTicker(rescan)
	defer scan.Stop()

	for {
		select {
		case <-stop:
			r.send(0)
			return
		case p := <-r.packets:
			if other, ok := r.handle(p); ok && r.probing {
				logf("%s is already taken by %s, so it is not announced", r.name, other)
				os.Exit(2)
			}
		case <-probe.C:
			if len(r.socks) == 0 {
				continue
			}
			if probesLeft > 0 {
				r.probe()
				probesLeft--
				continue
			}
			probe.Stop()
			r.probing = false
			logf("announcing %s as %s", r.name, r.describe())
			announcesLeft = 2
			announce.Reset(0)
		case <-announce.C:
			r.send(ttl)
			if announcesLeft--; announcesLeft > 0 {
				announce.Reset(time.Second)
			}
		case <-scan.C:
			if r.scan() && !r.probing {
				logf("addresses changed: %s is now %s", r.name, r.describe())
				announcesLeft = 2
				announce.Reset(0)
			}
		}
	}
}

// validLabel says whether s is a host name label a browser can type.
func validLabel(s string) bool {
	if len(s) == 0 || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func (r *responder) describe() string {
	var parts []string
	for _, iface := range sortedKeys(r.addrs) {
		for _, a := range r.addrs[iface] {
			parts = append(parts, a.String())
		}
	}
	if len(parts) == 0 {
		return "nothing yet"
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scan reads the home network interfaces and their addresses, opens a socket
// on each new interface and closes those of interfaces that are gone. It
// reports whether the addresses changed.
func (r *responder) scan() bool {
	found := map[string][]netip.Addr{}
	for _, name := range homeInterfaces() {
		ifi, err := net.InterfaceByName(name)
		if err != nil || ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		var usable []netip.Addr
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(ipnet.IP)
			if !ok {
				continue
			}
			// A link-local address needs a zone to be of use to a browser.
			if ip = ip.Unmap(); ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
				continue
			}
			usable = append(usable, ip)
		}
		if len(usable) == 0 {
			continue
		}
		sort.Slice(usable, func(i, j int) bool { return usable[i].Less(usable[j]) })
		found[name] = usable
		for _, v6 := range []bool{false, true} {
			key := fmt.Sprintf("%s/%t", name, v6)
			if _, open := r.socks[key]; open {
				continue
			}
			l, err := listen(ifi, v6)
			if err != nil {
				// A machine without IPv6 has no IPv6 socket to open.
				if !v6 {
					logf("cannot listen on %s: %v", name, err)
				}
				continue
			}
			r.socks[key] = l
			go l.read(r.packets)
		}
	}
	for key, l := range r.socks {
		if _, still := found[l.iface]; !still {
			l.conn.Close()
			delete(r.socks, key)
		}
	}
	changed := fmt.Sprint(found) != fmt.Sprint(r.addrs)
	r.addrs = found
	return changed
}

// homeInterfaces names the interfaces that have a default route: the home
// network. That leaves out the Docker and Supervisor bridges, the containers'
// veth pairs and Tailscale, whose addresses no phone or TV can reach.
func homeInterfaces() []string {
	seen := map[string]bool{}
	// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
	eachLine("/proc/net/route", func(f []string) {
		if len(f) >= 8 && f[1] == "00000000" && f[7] == "00000000" {
			seen[f[0]] = true
		}
	})
	// destination prefix source prefix next-hop metric refcnt use flags iface
	eachLine("/proc/net/ipv6_route", func(f []string) {
		if len(f) >= 10 && strings.Trim(f[0], "0") == "" && f[1] == "00" && f[9] != "lo" {
			seen[f[9]] = true
		}
	})
	return sortedKeys(seen)
}

func eachLine(path string, fn func(fields []string)) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	for lines := bufio.NewScanner(file); lines.Scan(); {
		fn(strings.Fields(lines.Text()))
	}
}

// listen opens the mDNS socket of one family on one interface. It is bound to
// the group address, joined on this interface only, and hears nothing that
// arrives for the group on another interface.
func listen(ifi *net.Interface, v6 bool) (*listener, error) {
	family, group := syscall.AF_INET, group4
	if v6 {
		family, group = syscall.AF_INET6, group6
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, syscall.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	set := func(level, opt, value int) {
		if err == nil {
			err = os.NewSyscallError("setsockopt", syscall.SetsockoptInt(fd, level, opt, value))
		}
	}
	// Both, as every other mDNS program on the machine sets them: the port is shared.
	set(syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	set(syscall.SOL_SOCKET, soReusePort, 1)
	if v6 {
		set(syscall.IPPROTO_IPV6, syscall.IPV6_V6ONLY, 1)
		set(syscall.IPPROTO_IPV6, ipv6MulticastAll, 0)
		set(syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_IF, ifi.Index)
		set(syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_HOPS, 255)
		set(syscall.IPPROTO_IPV6, syscall.IPV6_MULTICAST_LOOP, 1)
		if err == nil {
			mreq := &syscall.IPv6Mreq{Multiaddr: group.As16(), Interface: uint32(ifi.Index)}
			err = os.NewSyscallError("join", syscall.SetsockoptIPv6Mreq(fd, syscall.IPPROTO_IPV6, syscall.IPV6_JOIN_GROUP, mreq))
		}
		if err == nil {
			err = os.NewSyscallError("bind", syscall.Bind(fd, &syscall.SockaddrInet6{Port: port, Addr: group.As16(), ZoneId: uint32(ifi.Index)}))
		}
	} else {
		mreq := &syscall.IPMreqn{Multiaddr: group.As4(), Ifindex: int32(ifi.Index)}
		set(syscall.IPPROTO_IP, ipMulticastAll, 0)
		set(syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255)
		set(syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
		if err == nil {
			err = os.NewSyscallError("multicast interface", syscall.SetsockoptIPMreqn(fd, syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, mreq))
		}
		if err == nil {
			err = os.NewSyscallError("join", syscall.SetsockoptIPMreqn(fd, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq))
		}
		if err == nil {
			err = os.NewSyscallError("bind", syscall.Bind(fd, &syscall.SockaddrInet4{Port: port, Addr: group.As4()}))
		}
	}
	if err != nil {
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "mdns")
	defer file.Close()
	pc, err := net.FilePacketConn(file)
	if err != nil {
		return nil, err
	}
	to := netip.AddrPortFrom(group, port)
	if v6 {
		to = netip.AddrPortFrom(group.WithZone(ifi.Name), port)
	}
	return &listener{iface: ifi.Name, v6: v6, conn: pc.(*net.UDPConn), group: to}, nil
}

// write sends msg, and logs why it could not once, not at every packet.
func (l *listener) write(msg []byte, to netip.AddrPort) {
	_, err := l.conn.WriteToUDPAddrPort(msg, to)
	if err == nil || errors.Is(err, net.ErrClosed) {
		l.failed = ""
		return
	}
	if why := err.Error(); why != l.failed {
		l.failed = why
		logf("cannot send on %s: %v", l.iface, err)
	}
}

func (l *listener) read(into chan<- packet) {
	buf := make([]byte, 9000)
	for {
		n, src, err := l.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		into <- packet{from: l, src: src, data: bytes.Clone(buf[:n])}
	}
}

// handle answers a query for the name, and looks in a response for another
// machine that has the name. It returns that machine's address when it finds
// one.
func (r *responder) handle(p packet) (netip.Addr, bool) {
	msg, ok := parse(p.data)
	if debug {
		logf("debug: %d bytes on %s from %s: ok=%t response=%t questions=%v", len(p.data), p.from.iface, p.src, ok, msg.response, msg.questions)
	}
	if !ok {
		return netip.Addr{}, false
	}
	if msg.response {
		return r.conflict(p, msg)
	}
	if r.probing || msg.opcode != 0 {
		return netip.Addr{}, false
	}
	for _, q := range msg.questions {
		if q.name != r.name || q.class&^flush != classIN {
			continue
		}
		// A query from another port than 5353 is from a plain DNS client, which
		// expects an ordinary answer sent back to it alone.
		if p.src.Port() != port {
			reply := r.message(p.from.iface, msg.id, &q, 10, false)
			p.from.write(reply, p.src)
		} else if time.Since(p.from.sent) >= quiet {
			r.sendOn(p.from, ttl)
		}
		break
	}
	return netip.Addr{}, false
}

// conflict finds an address record for the name that is none of this
// machine's, sent by another machine.
func (r *responder) conflict(p packet, msg message) (netip.Addr, bool) {
	for _, rec := range msg.records {
		if rec.name != r.name || rec.ttl == 0 || (rec.kind != typeA && rec.kind != typeAAAA) {
			continue
		}
		addr, ok := netip.AddrFromSlice(rec.data)
		if !ok || r.mine(addr) {
			continue
		}
		if !r.probing && time.Since(r.warned) > time.Minute {
			r.warned = time.Now()
			logf("warning: %s also answers for %s, with %s", p.src.Addr(), r.name, addr)
		}
		return p.src.Addr().WithZone(""), true
	}
	return netip.Addr{}, false
}

func (r *responder) mine(addr netip.Addr) bool {
	for _, addrs := range r.addrs {
		for _, a := range addrs {
			if a == addr {
				return true
			}
		}
	}
	// A link-local address of this machine, which another responder on it
	// might announce for the same name.
	ifaces, _ := net.InterfaceAddrs()
	for _, a := range ifaces {
		if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.Equal(addr.AsSlice()) {
			return true
		}
	}
	return false
}

// probe asks who has the name, on every interface.
func (r *responder) probe() {
	q := appendName(make([]byte, 12, 64), r.labels)
	binary.BigEndian.PutUint16(q[4:], 1) // one question
	q = binary.BigEndian.AppendUint16(q, typeANY)
	q = binary.BigEndian.AppendUint16(q, classIN)
	for _, l := range r.socks {
		l.write(q, l.group)
	}
}

// send multicasts the records on every interface. A time to live of zero
// tells the network to forget them.
func (r *responder) send(seconds uint32) {
	if r.probing {
		return
	}
	for _, l := range r.socks {
		r.sendOn(l, seconds)
	}
}

func (r *responder) sendOn(l *listener, seconds uint32) {
	if len(r.addrs[l.iface]) == 0 {
		return
	}
	l.sent = time.Now()
	l.write(r.message(l.iface, 0, nil, seconds, true), l.group)
}

// message is a response with every address of the interface, and an NSEC
// record that says the name has no other kind of record, so nobody waits for
// one. The answer to a plain DNS client repeats its question and its ID.
func (r *responder) message(iface string, id uint16, q *question, seconds uint32, multicast bool) []byte {
	class := uint16(classIN)
	if multicast {
		class |= flush
	}
	msg := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(msg[0:], id)
	binary.BigEndian.PutUint16(msg[2:], 0x8400) // a response, authoritative
	if q != nil {
		binary.BigEndian.PutUint16(msg[4:], 1)
		msg = appendName(msg, r.labels)
		msg = binary.BigEndian.AppendUint16(msg, q.kind)
		msg = binary.BigEndian.AppendUint16(msg, q.class&^flush)
	}
	record := func(kind uint16, data []byte) {
		msg = appendName(msg, r.labels)
		msg = binary.BigEndian.AppendUint16(msg, kind)
		msg = binary.BigEndian.AppendUint16(msg, class)
		msg = binary.BigEndian.AppendUint32(msg, seconds)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(data)))
		msg = append(msg, data...)
	}
	count := uint16(0)
	have4, have6 := false, false
	for _, a := range r.addrs[iface] {
		if a.Is4() {
			record(typeA, a.AsSlice())
			have4 = true
		} else {
			record(typeAAAA, a.AsSlice())
			have6 = true
		}
		count++
	}
	binary.BigEndian.PutUint16(msg[6:], count)
	// NSEC, as an additional record: the next name is the name itself, then
	// the bitmap of the types it has, in window 0, without the bytes after the
	// last type. Bit 1 is A, bit 28 is AAAA.
	bitmap := []byte{0}
	if have4 {
		bitmap[0] |= 0x80 >> typeA
	}
	if have6 {
		bitmap = append(bitmap, 0, 0, 0x80>>(typeAAAA%8))
	}
	nsec := appendName(nil, r.labels)
	nsec = append(nsec, 0, byte(len(bitmap)))
	record(typeNSEC, append(nsec, bitmap...))
	binary.BigEndian.PutUint16(msg[10:], 1)
	return msg
}

func appendName(msg []byte, labels []string) []byte {
	for _, label := range labels {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	return append(msg, 0)
}

type question struct {
	name  string
	kind  uint16
	class uint16
}

type record struct {
	name string
	kind uint16
	ttl  uint32
	data []byte
}

type message struct {
	id        uint16
	response  bool
	opcode    byte
	questions []question
	records   []record
}

// parse reads a DNS message as far as it is well formed.
func parse(data []byte) (message, bool) {
	if len(data) < 12 {
		return message{}, false
	}
	flags := binary.BigEndian.Uint16(data[2:])
	msg := message{id: binary.BigEndian.Uint16(data), response: flags&0x8000 != 0, opcode: byte(flags >> 11 & 0xf)}
	questions := int(binary.BigEndian.Uint16(data[4:]))
	records := int(binary.BigEndian.Uint16(data[6:])) + int(binary.BigEndian.Uint16(data[8:])) + int(binary.BigEndian.Uint16(data[10:]))
	at := 12
	for i := 0; i < questions; i++ {
		name, next, ok := readName(data, at)
		if !ok || next+4 > len(data) {
			return msg, true
		}
		msg.questions = append(msg.questions, question{
			name:  name,
			kind:  binary.BigEndian.Uint16(data[next:]),
			class: binary.BigEndian.Uint16(data[next+2:]),
		})
		at = next + 4
	}
	for i := 0; i < records; i++ {
		name, next, ok := readName(data, at)
		if !ok || next+10 > len(data) {
			return msg, true
		}
		length := int(binary.BigEndian.Uint16(data[next+8:]))
		if next+10+length > len(data) {
			return msg, true
		}
		msg.records = append(msg.records, record{
			name: name,
			kind: binary.BigEndian.Uint16(data[next:]),
			ttl:  binary.BigEndian.Uint32(data[next+4:]),
			data: data[next+10 : next+10+length],
		})
		at = next + 10 + length
	}
	return msg, true
}

// readName reads the name at data[at:], lowercased and without the last dot,
// following compression pointers, and returns where what follows it starts.
func readName(data []byte, at int) (name string, next int, ok bool) {
	var labels []string
	next = -1
	for hops := 0; hops < 32; hops++ {
		if at >= len(data) {
			return "", 0, false
		}
		length := int(data[at])
		switch {
		case length == 0:
			if next < 0 {
				next = at + 1
			}
			return strings.ToLower(strings.Join(labels, ".")), next, true
		case length&0xc0 == 0xc0:
			if at+1 >= len(data) {
				return "", 0, false
			}
			if next < 0 {
				next = at + 2
			}
			at = int(binary.BigEndian.Uint16(data[at:]) & 0x3fff)
		case length&0xc0 != 0 || at+1+length > len(data):
			return "", 0, false
		default:
			labels = append(labels, string(data[at+1:at+1+length]))
			at += 1 + length
		}
	}
	return "", 0, false
}
