//go:build windows

package notify

import (
	"syscall"
	"unsafe"
)

var (
	winmm       = syscall.NewLazyDLL("winmm.dll")
	playSoundW  = winmm.NewProc("PlaySoundW")
	user32      = syscall.NewLazyDLL("user32.dll")
	messageBeep = user32.NewProc("MessageBeep")
)

const (
	sndAsync = 0x0001
	sndAlias = 0x00010000
)

func playChime() {
	soundName, err := syscall.UTF16PtrFromString("SystemNotification")
	if err == nil {
		r, _, _ := playSoundW.Call(uintptr(unsafe.Pointer(soundName)), 0, uintptr(sndAlias|sndAsync))
		if r != 0 {
			return
		}
	}
	// Fallback to standard asterisk / notification beep if PlaySound fails
	messageBeep.Call(uintptr(0x00000040))
}
