package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mode is the Windows projection mode ("Win+P") this app switches between.
type Mode string

const (
	ModeExtend   Mode = "extend"   // "Extend": the shared monitor is in use on this PC
	ModeInternal Mode = "internal" // "PC screen only": the shared monitor is released
)

func (m Mode) opposite() Mode {
	if m == ModeExtend {
		return ModeInternal
	}
	return ModeExtend
}

// planSwap decides what a hotkey press means, given this PC's current mode:
// whoever has the shared monitor gives it up and the other PC takes it.
func planSwap(local Mode) (newLocal, newPeer Mode) {
	newLocal = local.opposite()
	return newLocal, newLocal.opposite()
}

var errSuperseded = errors.New("superseded by a newer command")

const (
	discoverQuery = "MONITORSWAP?"
	discoverReply = "MONITORSWAP!"

	handshakeTimeout = 5 * time.Second
	pingInterval     = 5 * time.Second
	readTimeout      = 20 * time.Second
	writeTimeout     = 2 * time.Second
	ackTimeout       = 1500 * time.Millisecond
	retryDelay       = 3 * time.Second
)

// Link keeps one connection open between the two PCs. The desktop listens;
// the laptop only ever dials out, so a locked-down laptop needs no firewall
// change. Once connected, either side can send commands over the same socket.
//
// Wire protocol, one text line per message:
//
//	HELLO <keyhash> <name>   laptop -> desktop, first line
//	WELCOME <name>           desktop -> laptop, reply
//	SET <mode> <id>          either direction: "switch to this mode"
//	ACK <id>                 confirms a SET was received
//	PING                     keep-alive
type Link struct {
	Role string
	Port int
	Addr string // laptop: desktop name/IP ("" = discover by broadcast)
	Key  string
	Name string

	OnSet    func(Mode) // the other PC asked this PC to switch
	OnStatus func()     // connection came up or went down
	Logf     func(format string, args ...any)

	mu       sync.Mutex
	cur      *peerConn
	acks     map[string]chan struct{}
	seq      uint64
	listener net.Listener
	udp      *net.UDPConn
	closed   chan struct{}
	ready    chan struct{} // closed once the desktop is listening
	boundTo  int
}

type peerConn struct {
	c    net.Conn
	wmu  sync.Mutex
	name string
}

func (p *peerConn) writeLine(s string) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.c.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := p.c.Write([]byte(s + "\n"))
	return err
}

func (l *Link) logf(format string, args ...any) {
	if l.Logf != nil {
		l.Logf(format, args...)
	}
}

// Start begins listening (desktop) or connecting (laptop) in the background.
func (l *Link) Start() {
	l.init()
	if l.Role == roleDesktop {
		go l.runDesktop()
	} else {
		go l.runLaptop()
	}
}

func (l *Link) init() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed == nil {
		l.acks = map[string]chan struct{}{}
		l.closed = make(chan struct{})
		l.ready = make(chan struct{})
	}
}

// Close stops everything. Used on exit and in tests.
func (l *Link) Close() {
	l.init()
	l.mu.Lock()
	defer l.mu.Unlock()
	select {
	case <-l.closed:
		return
	default:
		close(l.closed)
	}
	if l.listener != nil {
		l.listener.Close()
	}
	if l.udp != nil {
		l.udp.Close()
	}
	if l.cur != nil {
		l.cur.c.Close()
	}
}

func (l *Link) isClosed() bool {
	select {
	case <-l.closed:
		return true
	default:
		return false
	}
}

// sleep waits for d, returning false if the link was closed meanwhile.
func (l *Link) sleep(d time.Duration) bool {
	select {
	case <-l.closed:
		return false
	case <-time.After(d):
		return true
	}
}

// Connected reports whether the other PC is connected, and its name.
func (l *Link) Connected() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cur == nil {
		return false, ""
	}
	return true, l.cur.name
}

// Send asks the other PC to switch mode. It returns false if the other PC
// is not connected or did not confirm in time.
func (l *Link) Send(m Mode) bool {
	l.init()
	l.mu.Lock()
	p := l.cur
	if p == nil {
		l.mu.Unlock()
		return false
	}
	l.seq++
	id := strconv.FormatUint(l.seq, 10)
	ch := make(chan struct{})
	l.acks[id] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.acks, id)
		l.mu.Unlock()
	}()

	if err := p.writeLine("SET " + string(m) + " " + id); err != nil {
		l.logf("send failed: %v", err)
		p.c.Close()
		return false
	}
	select {
	case <-ch:
		return true
	case <-time.After(ackTimeout):
		// Most likely the other PC went to sleep without closing the socket.
		l.logf("no confirmation from the other PC; dropping the connection")
		p.c.Close()
		return false
	}
}

func keyHash(key string) string {
	sum := sha256.Sum256([]byte("monitorswap:" + key))
	return hex.EncodeToString(sum[:])
}

func cleanName(s string) string {
	s = strings.Join(strings.Fields(s), "_")
	if s == "" {
		return "PC"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// isLocalNetwork accepts only addresses that cannot come from the internet:
// private/link-local ranges, or anything on a subnet this PC is directly
// attached to (covers home networks with unusual addressing).
func isLocalNetwork(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	if i := strings.IndexByte(host, '%'); i >= 0 { // IPv6 zone
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.Contains(ip) {
				ones, bits := ipn.Mask.Size()
				if bits-ones <= 16 && ones > 0 { // a real subnet, not a default route
					return true
				}
			}
		}
	}
	return false
}

// ---- desktop side ----

func (l *Link) runDesktop() {
	listenFailed := false
	for !l.isClosed() {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(l.Port))
		if err != nil {
			if !listenFailed {
				l.logf("cannot listen on port %d: %v (will keep trying)", l.Port, err)
			}
			listenFailed = true
			if !l.sleep(retryDelay) {
				return
			}
			continue
		}
		listenFailed = false
		port := ln.Addr().(*net.TCPAddr).Port
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err != nil {
			l.logf("cannot answer discovery on UDP %d: %v", port, err)
		}
		l.mu.Lock()
		l.listener, l.udp, l.boundTo = ln, udp, port
		l.mu.Unlock()
		if l.isClosed() {
			ln.Close()
			if udp != nil {
				udp.Close()
			}
			return
		}
		select {
		case <-l.ready:
		default:
			close(l.ready)
		}
		l.logf("listening on port %d", port)
		if udp != nil {
			go l.answerDiscovery(udp, port)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				break
			}
			go l.acceptPeer(c)
		}
		ln.Close()
		if udp != nil {
			udp.Close()
		}
		if !l.sleep(retryDelay) {
			return
		}
	}
}

func (l *Link) answerDiscovery(udp *net.UDPConn, port int) {
	buf := make([]byte, 256)
	for {
		n, from, err := udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if string(buf[:n]) != discoverQuery || !isLocalNetwork(from) {
			continue
		}
		udp.WriteToUDP([]byte(fmt.Sprintf("%s %s %d", discoverReply, cleanName(l.Name), port)), from)
	}
}

func (l *Link) acceptPeer(c net.Conn) {
	if !isLocalNetwork(c.RemoteAddr()) {
		l.logf("rejected connection from %s (not a local-network address)", c.RemoteAddr())
		c.Close()
		return
	}
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	sc := newScanner(c)
	if !sc.Scan() {
		c.Close()
		return
	}
	f := strings.Fields(sc.Text())
	if len(f) < 2 || f[0] != "HELLO" ||
		subtle.ConstantTimeCompare([]byte(f[1]), []byte(keyHash(l.Key))) != 1 {
		l.logf("rejected connection from %s (wrong key - \"key\" in settings.json must match on both PCs)", c.RemoteAddr())
		c.Write([]byte("DENIED\n"))
		c.Close()
		return
	}
	name := "laptop"
	if len(f) >= 3 {
		name = cleanName(f[2])
	}
	p := &peerConn{c: c, name: name}
	if err := p.writeLine("WELCOME " + cleanName(l.Name)); err != nil {
		c.Close()
		return
	}
	c.SetDeadline(time.Time{})
	l.serve(p, sc)
}

// ---- laptop side ----

func (l *Link) runLaptop() {
	last := ""
	for !l.isClosed() {
		err := l.dialOnce()
		if err == nil {
			last = ""
		} else if err.Error() != last { // log each distinct problem once, not every retry
			last = err.Error()
			l.logf("%v (will keep trying)", err)
		}
		if !l.sleep(retryDelay) {
			return
		}
	}
}

func (l *Link) dialOnce() error {
	addr := l.Addr
	if addr == "" {
		found, err := discover(l.Port, 2*time.Second)
		if err != nil {
			return fmt.Errorf("looking for the desktop: %v", err)
		}
		addr = found
	} else if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(l.Port))
	}
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return fmt.Errorf("connecting to %s: %v", addr, err)
	}
	c.SetDeadline(time.Now().Add(handshakeTimeout))
	p := &peerConn{c: c}
	if err := p.writeLine("HELLO " + keyHash(l.Key) + " " + cleanName(l.Name)); err != nil {
		c.Close()
		return fmt.Errorf("connecting to %s: %v", addr, err)
	}
	sc := newScanner(c)
	if !sc.Scan() {
		c.Close()
		return fmt.Errorf("connecting to %s: no reply", addr)
	}
	f := strings.Fields(sc.Text())
	if len(f) < 1 || f[0] != "WELCOME" {
		c.Close()
		if len(f) >= 1 && f[0] == "DENIED" {
			return errors.New(`the desktop refused the connection: "key" in settings.json must match on both PCs`)
		}
		return fmt.Errorf("connecting to %s: unexpected reply (is something else using port %d?)", addr, l.Port)
	}
	p.name = "desktop"
	if len(f) >= 2 {
		p.name = cleanName(f[1])
	}
	c.SetDeadline(time.Time{})
	l.logf("connected to %s at %s", p.name, addr)
	l.serve(p, sc)
	return nil
}

// discover broadcasts on the local network and returns "ip:port" of the
// desktop that answers. extra adds targets (used by tests).
func discover(port int, timeout time.Duration, extra ...*net.UDPAddr) (string, error) {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	targets := append([]*net.UDPAddr{{IP: net.IPv4bcast, Port: port}}, extra...)
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			mask := ipn.Mask
			if len(mask) == net.IPv6len {
				mask = mask[12:]
			}
			if ip4 == nil || ip4.IsLoopback() || len(mask) != net.IPv4len {
				continue
			}
			bc := make(net.IP, net.IPv4len)
			for i := range bc {
				bc[i] = ip4[i] | ^mask[i]
			}
			targets = append(targets, &net.UDPAddr{IP: bc, Port: port})
		}
	}
	sent := 0
	for _, t := range targets {
		if _, err := conn.WriteToUDP([]byte(discoverQuery), t); err == nil {
			sent++
		}
	}
	if sent == 0 {
		return "", errors.New("no network to search on")
	}
	conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 256)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", errors.New("no desktop answered on this network")
		}
		f := strings.Fields(string(buf[:n]))
		if len(f) < 1 || f[0] != discoverReply {
			continue
		}
		p := port
		if len(f) >= 3 {
			if v, err := strconv.Atoi(f[2]); err == nil && v > 0 && v < 65536 {
				p = v
			}
		}
		return net.JoinHostPort(from.IP.String(), strconv.Itoa(p)), nil
	}
}

// ---- shared ----

func newScanner(c net.Conn) *bufio.Scanner {
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 512), 512) // lines are short; refuse anything else
	return sc
}

// serve runs an established connection until it drops.
func (l *Link) serve(p *peerConn, sc *bufio.Scanner) {
	l.mu.Lock()
	if l.isClosed() {
		l.mu.Unlock()
		p.c.Close()
		return
	}
	old := l.cur
	l.cur = p
	l.mu.Unlock()
	if old != nil {
		old.c.Close() // a newer connection replaces a stale one
	}
	l.logf("other PC connected: %s", p.name)
	l.statusChanged()

	done := make(chan struct{})
	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if p.writeLine("PING") != nil {
					p.c.Close()
					return
				}
			}
		}
	}()

	for {
		p.c.SetReadDeadline(time.Now().Add(readTimeout))
		if !sc.Scan() {
			break
		}
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "SET":
			if len(f) < 3 {
				continue
			}
			m := Mode(f[1])
			if m != ModeExtend && m != ModeInternal {
				continue
			}
			p.writeLine("ACK " + f[2])
			if l.OnSet != nil {
				go l.OnSet(m)
			}
		case "ACK":
			if len(f) < 2 {
				continue
			}
			l.mu.Lock()
			ch := l.acks[f[1]]
			delete(l.acks, f[1])
			l.mu.Unlock()
			if ch != nil {
				close(ch)
			}
		}
	}
	close(done)
	p.c.Close()

	l.mu.Lock()
	wasCurrent := l.cur == p
	if wasCurrent {
		l.cur = nil
	}
	l.mu.Unlock()
	if wasCurrent {
		l.logf("other PC disconnected")
		l.statusChanged()
	}
}

func (l *Link) statusChanged() {
	if l.OnStatus != nil {
		l.OnStatus()
	}
}
