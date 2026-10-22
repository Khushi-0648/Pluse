//go:build !windows

package notify

import "fmt"

func playChime() {
	// Terminal bell on POSIX systems if attached to a TTY
	fmt.Print("\a")
}
