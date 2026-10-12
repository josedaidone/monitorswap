package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestParseHotkey(t *testing.T) {
	good := []struct {
		in   string
		mods uint32
		vk   uint32
	}{
		{"Pause", 0, 0x13},
		{"pause", 0, 0x13},
		{"Ctrl+Alt+M", modControl | modAlt, 'M'},
		{" ctrl + alt + p ", modControl | modAlt, 'P'},
		{"Win+Shift+F12", modWin | modShift, 0x7B},
		{"ScrollLock", 0, 0x91},
		{"Scroll Lock", 0, 0x91},
		{"Ctrl+Alt+1", modControl | modAlt, '1'},
		{"Ctrl+Numpad0", modControl, 0x60},
		{"F24", 0, 0x87},
	}
	for _, c := range good {
		mods, vk, err := parseHotkey(c.in)
		if err != nil || mods != c.mods || vk != c.vk {
			t.Errorf("parseHotkey(%q) = %#x, %#x, %v; want %#x, %#x", c.in, mods, vk, err, c.mods, c.vk)
		}
	}
	for _, bad := range []string{"", "Ctrl", "Ctrl+Alt", "Ctrl+Banana", "M+Ctrl", "F25", "F0", "Ctrl++M", "A+B"} {
		if _, _, err := parseHotkey(bad); err == nil {
			t.Errorf("parseHotkey(%q) should fail", bad)
		}
	}
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")

	// Written by Notepad with a byte-order mark, with fields left out.
	os.WriteFile(p, append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"role": "Laptop", "key": "abc"}`)...), 0o644)
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Role != roleLaptop || cfg.Hotkey != "Pause" || cfg.Port != defaultPort || !cfg.StartWithWindows || cfg.Key != "abc" {
		t.Errorf("defaults not applied: %+v", cfg)
	}

	for name, body := range map[string]string{
		"bad role":   `{"role": "server"}`,
		"bad hotkey": `{"role": "desktop", "hotkey": "Ctrl+Banana"}`,
		"bad port":   `{"role": "desktop", "port": 0}`,
		"bad json":   `{"role": "desktop",}`,
	} {
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := loadConfig(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	want := defaultConfig(roleDesktop)
	want.Key = "k"
	if err := saveConfig(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(p)
	if err != nil || got != want {
		t.Errorf("round trip: got %+v, %v; want %+v", got, err, want)
	}
}

// fakePC is one computer: a Link plus a pretend display.
type fakePC struct {
	t       *testing.T
	link    *Link
	mu      sync.Mutex
	mode    Mode
	noCable bool // extend fails, as if the monitor were unplugged
}

func (pc *fakePC) get() Mode {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	return pc.mode
}

func (pc *fakePC) apply(m Mode) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if m == ModeExtend && pc.noCable {
		return errors.New("no second screen")
	}
	pc.mode = m
	return nil
}

func (pc *fakePC) press(kind string) swapOutcome {
	return runSwap(kind, pc.get(), pc.link.Send, pc.apply)
}

func newPC(t *testing.T, role string, port int, addr, key string, mode Mode) *fakePC {
	pc := &fakePC{t: t, mode: mode}
	pc.link = &Link{Role: role, Port: port, Addr: addr, Key: key, Name: role + " pc",
		Logf:  func(f string, a ...any) { t.Logf("["+role+"] "+f, a...) },
		OnSet: func(m Mode) { pc.apply(m) },
	}
	pc.link.Start()
	t.Cleanup(pc.link.Close)
	return pc
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func desktopPort(t *testing.T, d *fakePC) int {
	t.Helper()
	select {
	case <-d.link.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("desktop never started listening")
	}
	d.link.mu.Lock()
	defer d.link.mu.Unlock()
	return d.link.boundTo
}

func pair(t *testing.T, desktopMode, laptopMode Mode) (desktop, laptop *fakePC) {
	t.Helper()
	desktop = newPC(t, roleDesktop, 0, "", "secret", desktopMode)
	port := desktopPort(t, desktop)
	laptop = newPC(t, roleLaptop, port, "127.0.0.1", "secret", laptopMode)
	waitFor(t, "both sides connected", 5*time.Second, func() bool {
		a, _ := desktop.link.Connected()
		b, _ := laptop.link.Connected()
		return a && b
	})
	return
}

func expect(t *testing.T, desktop, laptop *fakePC, wantDesktop, wantLaptop Mode) {
	t.Helper()
	waitFor(t, "desktop="+string(wantDesktop)+" laptop="+string(wantLaptop), 3*time.Second, func() bool {
		return desktop.get() == wantDesktop && laptop.get() == wantLaptop
	})
}

func TestSwapBackAndForth(t *testing.T) {
	desktop, laptop := pair(t, ModeExtend, ModeInternal)
	if _, name := desktop.link.Connected(); name != "laptop_pc" {
		t.Errorf("desktop sees peer name %q", name)
	}
	if _, name := laptop.link.Connected(); name != "desktop_pc" {
		t.Errorf("laptop sees peer name %q", name)
	}

	// Pressed on the laptop (the side that only dials out): monitor moves to the laptop.
	if out := laptop.press("swap"); !out.Reached || out.Err != nil {
		t.Fatalf("laptop press: %+v", out)
	}
	expect(t, desktop, laptop, ModeInternal, ModeExtend)

	// Pressed on the laptop again: monitor goes back to the desktop.
	laptop.press("swap")
	expect(t, desktop, laptop, ModeExtend, ModeInternal)

	// Pressed on the desktop: commands flow the other way over the same connection.
	if out := desktop.press("swap"); !out.Reached || out.Err != nil {
		t.Fatalf("desktop press: %+v", out)
	}
	expect(t, desktop, laptop, ModeInternal, ModeExtend)
	desktop.press("swap")
	expect(t, desktop, laptop, ModeExtend, ModeInternal)

	// Explicit take/give are idempotent.
	desktop.press("take")
	expect(t, desktop, laptop, ModeExtend, ModeInternal)
	desktop.press("give")
	expect(t, desktop, laptop, ModeInternal, ModeExtend)
	laptop.press("take")
	expect(t, desktop, laptop, ModeInternal, ModeExtend)
}

func TestOutOfSyncStatesAreRepaired(t *testing.T) {
	// Both extended (e.g. someone used Win+P by hand): one press fixes it.
	desktop, laptop := pair(t, ModeExtend, ModeExtend)
	laptop.press("swap")
	expect(t, desktop, laptop, ModeExtend, ModeInternal)

	// Both released.
	desktop2, laptop2 := pair(t, ModeInternal, ModeInternal)
	desktop2.press("swap")
	expect(t, desktop2, laptop2, ModeExtend, ModeInternal)
}

func TestOtherPCUnreachable(t *testing.T) {
	// Laptop with no desktop on the network: it still switches itself, quickly.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens here now
	laptop := newPC(t, roleLaptop, port, "127.0.0.1", "secret", ModeInternal)
	start := time.Now()
	out := laptop.press("swap")
	if out.Reached || out.Err != nil || laptop.get() != ModeExtend {
		t.Fatalf("unexpected outcome %+v, mode %s", out, laptop.get())
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("took %v; should not wait when the other PC is not connected", d)
	}
}

func TestOtherPCGoesAwayAndComesBack(t *testing.T) {
	desktop, laptop := pair(t, ModeExtend, ModeInternal)
	port := desktopPort(t, desktop)

	desktop.link.Close() // desktop shut down
	waitFor(t, "laptop notices the desktop is gone", 5*time.Second, func() bool {
		c, _ := laptop.link.Connected()
		return !c
	})
	if out := laptop.press("swap"); out.Reached || laptop.get() != ModeExtend {
		t.Fatalf("laptop should switch alone: %+v", out)
	}

	// Desktop boots again on the same port: the laptop reconnects by itself.
	desktop2 := newPC(t, roleDesktop, port, "", "secret", ModeExtend)
	waitFor(t, "laptop reconnects", 15*time.Second, func() bool {
		a, _ := desktop2.link.Connected()
		b, _ := laptop.link.Connected()
		return a && b
	})
	laptop.press("give")
	expect(t, desktop2, laptop, ModeExtend, ModeInternal)
}

func TestSleepingPeerDoesNotHangTheHotkey(t *testing.T) {
	// A "desktop" that completes the handshake and then goes silent, like a
	// PC that fell asleep with the socket still open.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 256)
		c.Read(buf)
		c.Write([]byte("WELCOME zombie\n"))
		time.Sleep(10 * time.Second)
		c.Close()
	}()
	laptop := newPC(t, roleLaptop, ln.Addr().(*net.TCPAddr).Port, "127.0.0.1", "secret", ModeInternal)
	waitFor(t, "connected", 5*time.Second, func() bool { c, _ := laptop.link.Connected(); return c })

	start := time.Now()
	out := laptop.press("swap")
	if out.Reached || laptop.get() != ModeExtend {
		t.Fatalf("unexpected outcome %+v", out)
	}
	if d := time.Since(start); d > ackTimeout+time.Second {
		t.Errorf("hotkey blocked for %v", d)
	}
}

func TestWrongKeyIsRejected(t *testing.T) {
	desktop := newPC(t, roleDesktop, 0, "", "right", ModeExtend)
	port := desktopPort(t, desktop)
	laptop := newPC(t, roleLaptop, port, "127.0.0.1", "wrong", ModeInternal)
	time.Sleep(1500 * time.Millisecond)
	if c, _ := desktop.link.Connected(); c {
		t.Error("desktop accepted a laptop with the wrong key")
	}
	if c, _ := laptop.link.Connected(); c {
		t.Error("laptop thinks it is connected")
	}
	// And raw garbage / unauthenticated commands do nothing.
	c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	c.Write([]byte("SET internal 1\n"))
	time.Sleep(300 * time.Millisecond)
	c.Close()
	if desktop.get() != ModeExtend {
		t.Error("unauthenticated command changed the display mode")
	}
}

func TestMonitorNotDetectedHandsItBack(t *testing.T) {
	desktop, laptop := pair(t, ModeExtend, ModeInternal)
	laptop.noCable = true
	out := laptop.press("swap")
	if out.Err == nil || !out.HandedBack {
		t.Fatalf("unexpected outcome %+v", out)
	}
	expect(t, desktop, laptop, ModeExtend, ModeInternal)
}

func TestDiscovery(t *testing.T) {
	desktop := newPC(t, roleDesktop, 0, "", "secret", ModeExtend)
	port := desktopPort(t, desktop)
	got, err := discover(port, 2*time.Second, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	host, p, _ := net.SplitHostPort(got)
	if p != strconv.Itoa(port) || net.ParseIP(host) == nil {
		t.Errorf("discover returned %q, want <ip>:%d", got, port)
	}
	if _, err := discover(1, 300*time.Millisecond); err == nil {
		t.Error("discover should fail when nothing answers")
	}
}

func TestIsLocalNetwork(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.20:5000": true, "10.0.0.7:1": true, "172.16.5.5:1": true,
		"127.0.0.1:1": true, "169.254.10.10:1": true, "[fe80::1%eth0]:1": true,
		"8.8.8.8:1": false, "172.32.0.1:1": false, "[2001:4860::1]:1": false,
	} {
		tcp, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			// zone may not resolve on this machine; test the string form directly
			if got := isLocalNetwork(strAddr(addr)); got != want {
				t.Errorf("isLocalNetwork(%s) = %v", addr, got)
			}
			continue
		}
		if got := isLocalNetwork(tcp); got != want {
			t.Errorf("isLocalNetwork(%s) = %v", addr, got)
		}
	}
}

type strAddr string

func (s strAddr) Network() string { return "tcp" }
func (s strAddr) String() string  { return string(s) }
