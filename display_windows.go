//go:build windows

package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Display switching uses the same Windows API that the Win+P panel uses
// (SetDisplayConfig with a topology flag), so the result is identical to
// picking "PC screen only" or "Extend" by hand.

const (
	topologyInternal = 0x1 // PC screen only
	topologyClone    = 0x2 // Duplicate
	topologyExtend   = 0x4 // Extend
	topologyExternal = 0x8 // Second screen only

	sdcApply = 0x80

	qdcOnlyActivePaths = 0x2
	qdcDatabaseCurrent = 0x4

	errorInsufficientBuffer = 122

	sizeofPathInfo = 72 // DISPLAYCONFIG_PATH_INFO
	sizeofModeInfo = 64 // DISPLAYCONFIG_MODE_INFO
)

var (
	procSetDisplayConfig            = user32.NewProc("SetDisplayConfig")
	procQueryDisplayConfig          = user32.NewProc("QueryDisplayConfig")
	procGetDisplayConfigBufferSizes = user32.NewProc("GetDisplayConfigBufferSizes")

	displayMu sync.Mutex
	applyGen  atomic.Uint64
)

// currentTopology returns the active Win+P mode as a topology* constant.
func currentTopology() (uint32, error) {
	var code uintptr
	for attempt := 0; attempt < 3; attempt++ {
		var nPaths, nModes uint32
		code, _, _ = procGetDisplayConfigBufferSizes.Call(qdcDatabaseCurrent,
			uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&nModes)))
		if code != 0 {
			break
		}
		// Windows only fills these buffers; we never read them, so plain
		// byte storage of the right size is enough.
		paths := make([]uint64, (int(nPaths)+2)*sizeofPathInfo/8)
		modes := make([]uint64, (int(nModes)+2)*sizeofModeInfo/8)
		var topology uint32
		code, _, _ = procQueryDisplayConfig.Call(qdcDatabaseCurrent,
			uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&paths[0])),
			uintptr(unsafe.Pointer(&nModes)), uintptr(unsafe.Pointer(&modes[0])),
			uintptr(unsafe.Pointer(&topology)))
		if code == 0 {
			return topology, nil
		}
		if code != errorInsufficientBuffer { // changed between the two calls: retry
			break
		}
	}
	return 0, fmt.Errorf("QueryDisplayConfig failed (code %d)", code)
}

// activeDisplayCount is a fallback for currentMode.
func activeDisplayCount() int {
	var nPaths, nModes uint32
	code, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths,
		uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&nModes)))
	if code != 0 {
		return 1
	}
	return int(nPaths)
}

// currentMode reports whether this PC is currently using the shared monitor.
// Anything other than "PC screen only" counts as using it.
func currentMode() Mode {
	t, err := currentTopology()
	if err != nil {
		if activeDisplayCount() > 1 {
			return ModeExtend
		}
		return ModeInternal
	}
	if t == topologyInternal {
		return ModeInternal
	}
	return ModeExtend
}

func isExactly(target Mode) (match bool, known bool) {
	t, err := currentTopology()
	if err != nil {
		return false, false
	}
	if target == ModeExtend {
		return t == topologyExtend, true
	}
	return t == topologyInternal, true
}

// applyMode switches the projection mode and confirms that it took effect.
// "Extend" is retried for a few seconds, because some monitors only show up
// on an input once the other PC has stopped sending a signal.
func applyMode(target Mode, logf func(string, ...any)) error {
	gen := applyGen.Add(1) // a newer call cancels an older one that is still retrying
	displayMu.Lock()
	defer displayMu.Unlock()

	flag := uintptr(topologyInternal)
	if target == ModeExtend {
		flag = topologyExtend
	}
	var lastCode uintptr
	for attempt := 0; attempt < 15; attempt++ {
		if applyGen.Load() != gen {
			return errSuperseded
		}
		if ok, known := isExactly(target); known && ok {
			return nil
		}
		lastCode, _, _ = procSetDisplayConfig.Call(0, 0, 0, 0, sdcApply|flag)
		time.Sleep(350 * time.Millisecond)
		ok, known := isExactly(target)
		if ok || (!known && lastCode == 0) {
			return nil
		}
		if attempt == 0 {
			logf("switch to %s did not take effect yet (code %d); retrying", target, lastCode)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if target == ModeExtend {
		return fmt.Errorf("Windows could not extend to a second screen (code %d). Is the monitor cable connected?", lastCode)
	}
	return fmt.Errorf("Windows could not switch to PC screen only (code %d)", lastCode)
}
