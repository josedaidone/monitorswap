//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	appName     = "MonitorSwap"
	windowClass = "MonitorSwapHiddenWindow"
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procGetModuleHandle       = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassEx       = user32.NewProc("RegisterClassExW")
	procCreateWindowEx        = user32.NewProc("CreateWindowExW")
	procDefWindowProc         = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetMessage            = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessage       = user32.NewProc("DispatchMessageW")
	procPostMessage           = user32.NewProc("PostMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procFindWindow            = user32.NewProc("FindWindowW")
	procRegisterWindowMessage = user32.NewProc("RegisterWindowMessageW")
	procRegisterHotKey        = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey      = user32.NewProc("UnregisterHotKey")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenu            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procMessageBox            = user32.NewProc("MessageBoxW")
	procCreateIconIndirect    = user32.NewProc("CreateIconIndirect")
	procSetDpiAwarenessCtx    = user32.NewProc("SetProcessDpiAwarenessContext")
	procShellNotifyIcon       = shell32.NewProc("Shell_NotifyIconW")
	procShellExecute          = shell32.NewProc("ShellExecuteW")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap          = gdi32.NewProc("CreateBitmap")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procRegCreateKeyEx        = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueEx         = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValue        = advapi32.NewProc("RegDeleteValueW")
	procRegCloseKey           = advapi32.NewProc("RegCloseKey")
)

const (
	wmDestroy       = 0x0002
	wmDisplayChange = 0x007E
	wmHotkey        = 0x0312
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000

	wmTray    = wmApp + 1 // tray icon mouse events
	wmRefresh = wmApp + 2 // state changed: redraw icon/tooltip
	wmBalloon = wmApp + 3 // show queued notifications
	wmCmdSwap = wmApp + 10
	wmCmdTake = wmApp + 11
	wmCmdGive = wmApp + 12

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 0x1
	nifIcon   = 0x2
	nifTip    = 0x4
	nifInfo   = 0x10
	niifInfo  = 0x1
	niifWarn  = 0x2

	mfString    = 0x0
	mfGrayed    = 0x1
	mfChecked   = 0x8
	mfSeparator = 0x800

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020
	tpmNoNotify    = 0x0080
	tpmReturnCmd   = 0x0100

	mbIconError    = 0x10
	mbIconQuestion = 0x20
	mbIconInfo     = 0x40
	mbYesNoCancel  = 0x3
	idYes          = 6
	idNo           = 7

	menuSwap     = 1
	menuTake     = 2
	menuGive     = 3
	menuStartup  = 4
	menuSettings = 5
	menuRestart  = 6
	menuLog      = 7
	menuExit     = 9

	hotkeyID = 1
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type notifyIconData struct {
	Size            uint32
	HWnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        [16]byte
	BalloonIcon     uintptr
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type iconInfo struct {
	IsIcon   int32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

type balloon struct {
	title, text string
	warn        bool
}

type app struct {
	dir     string
	cfgPath string
	cfg     Config
	link    *Link
	log     *logger

	hwnd           uintptr
	iconOn         uintptr
	iconOff        uintptr
	taskbarCreated uint32
	hotkeyOK       bool
	restart        bool

	busy sync.Mutex // one swap at a time; extra key presses are ignored

	mu       sync.Mutex
	balloons []balloon
}

func utf16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("?")
	}
	return p
}

func copyUTF16(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

func messageBox(text string, flags uintptr) int {
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(utf16(text))),
		uintptr(unsafe.Pointer(utf16(appName))), flags)
	return int(r)
}

func findRunningInstance() uintptr {
	h, _, _ := procFindWindow.Call(uintptr(unsafe.Pointer(utf16(windowClass))), 0)
	return h
}

func main() {
	runtime.LockOSThread() // the window and its message loop must share one thread

	if procSetDpiAwarenessCtx.Find() == nil {
		procSetDpiAwarenessCtx.Call(^uintptr(3)) // per-monitor v2 (-4): crisp menus
	}

	// Command-line use: "MonitorSwap.exe swap|take|give" tells the running copy what to do.
	command := ""
	if len(os.Args) > 1 {
		command = strings.ToLower(strings.TrimLeft(os.Args[1], "-/"))
	}
	running := findRunningInstance()
	commands := map[string]uintptr{"swap": wmCmdSwap, "take": wmCmdTake, "give": wmCmdGive}
	if command != "" {
		m, ok := commands[command]
		switch {
		case !ok:
			messageBox("Usage:\n\nMonitorSwap.exe          start the tray app\nMonitorSwap.exe swap   swap the shared monitor\nMonitorSwap.exe take   use the shared monitor on this PC\nMonitorSwap.exe give   send the shared monitor to the other PC", mbIconInfo)
			os.Exit(2)
		case running == 0:
			messageBox("MonitorSwap is not running on this PC. Start it first (double-click MonitorSwap.exe).", mbIconError)
			os.Exit(1)
		}
		procPostMessage.Call(running, m, 0, 0)
		return
	}
	if running != 0 {
		return // already running; nothing to do
	}

	exe, err := os.Executable()
	if err != nil {
		messageBox("Cannot find my own location: "+err.Error(), mbIconError)
		os.Exit(1)
	}
	a := &app{dir: filepath.Dir(exe)}
	a.cfgPath = filepath.Join(a.dir, "settings.json")
	a.log = newLogger(filepath.Join(a.dir, "monitorswap.log"))

	cfg, err := loadConfig(a.cfgPath)
	if os.IsNotExist(err) {
		cfg, err = firstRun(a.cfgPath)
		if err != nil {
			return
		}
	}
	if err != nil {
		messageBox("Problem with settings.json:\n\n"+err.Error()+"\n\nFix the file (or delete it to start fresh) and run MonitorSwap again.", mbIconError)
		os.Exit(1)
	}
	a.cfg = cfg
	a.log.printf("---- started: role=%s hotkey=%q port=%d ----", cfg.Role, cfg.Hotkey, cfg.Port)

	name, _ := os.Hostname()
	a.link = &Link{
		Role: cfg.Role, Port: cfg.Port, Addr: cfg.DesktopAddress, Key: cfg.Key, Name: name,
		Logf:     a.log.printf,
		OnStatus: a.refresh,
		OnSet: func(m Mode) {
			a.log.printf("other PC asked: switch to %s", m)
			if err := applyMode(m, a.log.printf); err != nil && err != errSuperseded {
				a.log.printf("%v", err)
			}
			a.refresh()
		},
	}

	a.createWindow()
	a.iconOn = makeIcon(true)
	a.iconOff = makeIcon(false)
	a.addTrayIcon()
	a.registerHotkey()
	if err := setStartWithWindows(cfg.StartWithWindows, exe); err != nil {
		a.log.printf("could not update the start-with-Windows setting: %v", err)
	}
	a.link.Start()

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}

	a.link.Close()
	a.log.printf("exiting")
	if a.restart {
		procShellExecute.Call(0, uintptr(unsafe.Pointer(utf16("open"))),
			uintptr(unsafe.Pointer(utf16(exe))), 0, uintptr(unsafe.Pointer(utf16(a.dir))), 1)
	}
}

// firstRun creates settings.json when it is missing.
func firstRun(path string) (Config, error) {
	answer := messageBox("Welcome to MonitorSwap.\n\nIs this the DESKTOP PC?\n\nYes  =  desktop (waits for the laptop to connect)\nNo   =  laptop (connects to the desktop)", mbYesNoCancel|mbIconQuestion)
	var role string
	switch answer {
	case idYes:
		role = roleDesktop
	case idNo:
		role = roleLaptop
	default:
		return Config{}, fmt.Errorf("cancelled")
	}
	cfg := defaultConfig(role)
	if err := saveConfig(path, cfg); err != nil {
		messageBox("Could not create settings.json next to the program:\n\n"+err.Error()+"\n\nMove MonitorSwap to a folder you can write to, such as Documents.", mbIconError)
		return cfg, err
	}
	return cfg, nil
}

func (a *app) createWindow() {
	instance, _, _ := procGetModuleHandle.Call(0)
	class := utf16(windowClass)
	wc := wndClassEx{
		WndProc:   syscall.NewCallback(a.wndProc),
		Instance:  instance,
		ClassName: class,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	// A normal top-level window that is simply never shown: unlike a
	// message-only window it receives WM_DISPLAYCHANGE and can be found by
	// a second copy of the program.
	a.hwnd, _, _ = procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16(appName))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if a.hwnd == 0 {
		messageBox("Could not create the program window.", mbIconError)
		os.Exit(1)
	}
	r, _, _ := procRegisterWindowMessage.Call(uintptr(unsafe.Pointer(utf16("TaskbarCreated"))))
	a.taskbarCreated = uint32(r)
}

func (a *app) wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmHotkey, wmCmdSwap:
		a.act("swap")
		return 0
	case wmCmdTake:
		a.act("take")
		return 0
	case wmCmdGive:
		a.act("give")
		return 0
	case wmTray:
		if lParam == wmLButtonUp || lParam == wmRButtonUp {
			a.showMenu()
		}
		return 0
	case wmRefresh, wmDisplayChange:
		a.updateTrayIcon()
	case wmBalloon:
		a.showBalloons()
		return 0
	case wmDestroy:
		a.removeTrayIcon()
		procUnregisterHotKey.Call(hwnd, hotkeyID)
		procPostQuitMessage.Call(0)
		return 0
	default:
		if a.taskbarCreated != 0 && message == a.taskbarCreated {
			a.addTrayIcon() // Explorer restarted and forgot our icon
			return 0
		}
	}
	r, _, _ := procDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

// refresh may be called from any goroutine.
func (a *app) refresh() {
	procPostMessage.Call(a.hwnd, wmRefresh, 0, 0)
}

// notify may be called from any goroutine.
func (a *app) notify(title, text string, warn bool) {
	a.log.printf("%s: %s", title, text)
	a.mu.Lock()
	a.balloons = append(a.balloons, balloon{title, text, warn})
	a.mu.Unlock()
	procPostMessage.Call(a.hwnd, wmBalloon, 0, 0)
}

// act handles a hotkey press, tray menu choice or command-line command.
func (a *app) act(kind string) {
	go func() {
		if !a.busy.TryLock() {
			return
		}
		defer a.busy.Unlock()

		local := currentMode()
		a.log.printf("%s requested (this PC is currently %s)", kind, local)
		out := runSwap(kind, local, a.link.Send, func(m Mode) error {
			return applyMode(m, a.log.printf)
		})
		switch {
		case out.Err != nil && out.HandedBack:
			a.notify("Could not switch", out.Err.Error()+" The monitor stays with the other PC.", true)
		case out.Err != nil:
			a.notify("Could not switch", out.Err.Error(), true)
		case !out.Reached:
			a.notify("Other PC not reachable", "Switched this PC only.", false)
		default:
			a.log.printf("done: this PC -> %s, other PC -> %s", out.NewLocal, out.NewLocal.opposite())
		}
		a.refresh()
	}()
}

// ---- tray icon ----

func (a *app) trayData(flags uint32) *notifyIconData {
	d := &notifyIconData{HWnd: a.hwnd, ID: 1, Flags: flags}
	d.Size = uint32(unsafe.Sizeof(*d))
	return d
}

func (a *app) describe() (tip string, icon uintptr, mode Mode) {
	mode = currentMode()
	connected, peer := a.link.Connected()
	tip = appName + ": shared monitor is "
	if mode == ModeExtend {
		tip += "on this PC"
		icon = a.iconOn
	} else {
		tip += "released"
		icon = a.iconOff
	}
	if connected {
		tip += "\nConnected to " + peer
	} else {
		tip += "\nOther PC not connected"
	}
	return
}

func (a *app) addTrayIcon() {
	d := a.trayData(nifMsg | nifIcon | nifTip)
	d.CallbackMessage = wmTray
	d.Icon = a.iconOff
	copyUTF16(d.Tip[:], appName)
	procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(d)))
	a.updateTrayIcon()
}

func (a *app) updateTrayIcon() {
	tip, icon, _ := a.describe()
	d := a.trayData(nifIcon | nifTip)
	d.Icon = icon
	copyUTF16(d.Tip[:], tip)
	procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(d)))
}

func (a *app) removeTrayIcon() {
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(a.trayData(0))))
}

func (a *app) showBalloons() {
	a.mu.Lock()
	list := a.balloons
	a.balloons = nil
	a.mu.Unlock()
	for _, b := range list {
		d := a.trayData(nifInfo)
		copyUTF16(d.InfoTitle[:], b.title)
		copyUTF16(d.Info[:], b.text)
		d.InfoFlags = niifInfo
		if b.warn {
			d.InfoFlags = niifWarn
		}
		procShellNotifyIcon.Call(nimModify, uintptr(unsafe.Pointer(d)))
	}
}

func (a *app) registerHotkey() {
	if a.cfg.Hotkey == "" {
		return
	}
	mods, vk, err := parseHotkey(a.cfg.Hotkey)
	if err == nil {
		r, _, _ := procRegisterHotKey.Call(a.hwnd, hotkeyID, uintptr(mods|modNoRepeat), uintptr(vk))
		a.hotkeyOK = r != 0
	}
	if !a.hotkeyOK {
		a.notify("Hotkey not available",
			fmt.Sprintf("%q is already used by another program. Pick another in settings.json (tray icon > Edit settings).", a.cfg.Hotkey), true)
	}
}

func (a *app) showMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	add := func(id uintptr, flags uintptr, text string) {
		procAppendMenu.Call(menu, flags, id, uintptr(unsafe.Pointer(utf16(text))))
	}
	sep := func() { procAppendMenu.Call(menu, mfSeparator, 0, 0) }

	_, _, mode := a.describe()
	connected, peer := a.link.Connected()
	if mode == ModeExtend {
		add(0, mfGrayed, "Shared monitor: on this PC")
	} else {
		add(0, mfGrayed, "Shared monitor: released")
	}
	switch {
	case connected:
		add(0, mfGrayed, "Other PC: connected ("+peer+")")
	case a.cfg.Role == roleLaptop:
		add(0, mfGrayed, "Other PC: looking for the desktop...")
	default:
		add(0, mfGrayed, "Other PC: waiting for the laptop...")
	}
	sep()
	swap := "Swap now"
	if a.hotkeyOK {
		swap += "\t" + a.cfg.Hotkey
	}
	add(menuSwap, mfString, swap)
	add(menuTake, mfString, "Use shared monitor on this PC")
	add(menuGive, mfString, "Send shared monitor to the other PC")
	sep()
	startup := uintptr(mfString)
	if a.cfg.StartWithWindows {
		startup |= mfChecked
	}
	add(menuStartup, startup, "Start with Windows")
	add(menuSettings, mfString, "Edit settings...")
	add(menuLog, mfString, "Open log")
	add(menuRestart, mfString, "Restart (apply settings)")
	add(0, mfGrayed, "This PC: "+a.selfDescription())
	sep()
	add(menuExit, mfString, "Exit")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(a.hwnd) // so the menu closes when clicking elsewhere
	choice, _, _ := procTrackPopupMenu.Call(menu,
		tpmRightButton|tpmBottomAlign|tpmNoNotify|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y), 0, a.hwnd, 0)
	procPostMessage.Call(a.hwnd, 0, 0, 0)

	switch choice {
	case menuSwap:
		a.act("swap")
	case menuTake:
		a.act("take")
	case menuGive:
		a.act("give")
	case menuStartup:
		a.cfg.StartWithWindows = !a.cfg.StartWithWindows
		exe, _ := os.Executable()
		if err := setStartWithWindows(a.cfg.StartWithWindows, exe); err != nil {
			a.notify("Start with Windows", "Could not change the setting: "+err.Error(), true)
		}
		if err := saveConfig(a.cfgPath, a.cfg); err != nil {
			a.log.printf("could not save settings: %v", err)
		}
	case menuSettings:
		a.openInNotepad(a.cfgPath)
	case menuLog:
		a.openInNotepad(a.log.path)
	case menuRestart:
		a.restart = true
		procDestroyWindow.Call(a.hwnd)
	case menuExit:
		procDestroyWindow.Call(a.hwnd)
	}
}

func (a *app) openInNotepad(path string) {
	procShellExecute.Call(0, uintptr(unsafe.Pointer(utf16("open"))),
		uintptr(unsafe.Pointer(utf16("notepad.exe"))),
		uintptr(unsafe.Pointer(utf16(`"`+path+`"`))), 0, 1)
}

// selfDescription is shown in the menu so the desktop's name/IP is easy to
// copy into the laptop's settings if automatic discovery is blocked.
func (a *app) selfDescription() string {
	name, _ := os.Hostname()
	var ips []string
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
					ips = append(ips, ip.String())
				}
			}
		}
	}
	if len(ips) > 3 {
		ips = ips[:3]
	}
	if len(ips) == 0 {
		return name
	}
	return name + "  (" + strings.Join(ips, ", ") + ")"
}

// ---- icon drawn in code, so the program is a single file ----

func makeIcon(active bool) uintptr {
	const size = 32
	px := make([]byte, size*size*4)               // BGRA, top-down
	r, g, b := byte(0x9A), byte(0xA0), byte(0xA6) // grey outline: monitor released
	if active {
		r, g, b = 0x2F, 0x8F, 0xFF // solid blue: monitor on this PC
	}
	set := func(x, y int) {
		i := (y*size + x) * 4
		px[i], px[i+1], px[i+2], px[i+3] = b, g, r, 0xFF
	}
	// Screen: x 2..29, y 4..22, with clipped corners.
	for y := 4; y <= 22; y++ {
		for x := 2; x <= 29; x++ {
			corner := (x == 2 || x == 29) && (y == 4 || y == 22)
			border := x < 5 || x > 26 || y < 7 || y > 19
			if !corner && (active || border) {
				set(x, y)
			}
		}
	}
	// Stand.
	for y := 23; y <= 25; y++ {
		for x := 14; x <= 17; x++ {
			set(x, y)
		}
	}
	for y := 26; y <= 28; y++ {
		for x := 9; x <= 22; x++ {
			set(x, y)
		}
	}

	bi := bitmapInfoHeader{Width: size, Height: -size, Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	copy(unsafe.Slice((*byte)(bits), len(px)), px)
	maskBits := make([]byte, size*size/8)
	mask, _, _ := procCreateBitmap.Call(size, size, 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	info := iconInfo{IsIcon: 1, Mask: mask, Color: color}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	procDeleteObject.Call(color)
	procDeleteObject.Call(mask)
	return icon
}

// ---- start with Windows (per-user registry entry; needs no admin rights) ----

func setStartWithWindows(enable bool, exe string) error {
	const hkeyCurrentUser = 0x80000001
	const keySetValue = 0x0002
	const regSZ = 1
	var key uintptr
	code, _, _ := procRegCreateKeyEx.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(utf16(runKey))),
		0, 0, 0, keySetValue, 0, uintptr(unsafe.Pointer(&key)), 0)
	if code != 0 {
		return fmt.Errorf("registry error %d", code)
	}
	defer procRegCloseKey.Call(key)
	name := uintptr(unsafe.Pointer(utf16(appName)))
	if !enable {
		code, _, _ = procRegDeleteValue.Call(key, name)
		if code != 0 && code != 2 { // 2 = was not there
			return fmt.Errorf("registry error %d", code)
		}
		return nil
	}
	value, err := syscall.UTF16FromString(`"` + exe + `"`)
	if err != nil {
		return err
	}
	code, _, _ = procRegSetValueEx.Call(key, name, 0, regSZ,
		uintptr(unsafe.Pointer(&value[0])), uintptr(len(value)*2))
	if code != 0 {
		return fmt.Errorf("registry error %d", code)
	}
	return nil
}

// ---- log file next to the exe, for troubleshooting ----

type logger struct {
	mu   sync.Mutex
	path string
}

func newLogger(path string) *logger {
	if st, err := os.Stat(path); err == nil && st.Size() > 512*1024 {
		os.Remove(path) // start over rather than grow forever
	}
	return &logger{path: path}
}

func (l *logger) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}
