//go:build windows

package main

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procMessageBox = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")

// showError uses a dialog, because the GUI build has no console to print to.
func showError(err error) {
	fmt.Fprintln(os.Stderr, "landlord:", err)
	title, _ := windows.UTF16PtrFromString("LANdlord")
	text, _ := windows.UTF16PtrFromString("LANdlord couldn't continue:\n\n" + err.Error())
	const mbIconError = 0x10
	procMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
