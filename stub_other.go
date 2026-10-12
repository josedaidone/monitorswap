//go:build !windows

package main

import "fmt"

// MonitorSwap is a Windows program. This stub only exists so the
// platform-independent parts (networking, hotkey parsing) can be tested on
// other systems with "go test".
func main() {
	fmt.Println("MonitorSwap only runs on Windows. Build with: ./package.sh")
}
