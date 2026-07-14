package main

import (
	"os/exec"
	"strings"
)

// copyToClipboard puts text on the macOS pasteboard via pbcopy, which avoids
// pulling in a cgo clipboard dependency for one menu item.
func copyToClipboard(text string) {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	cmd.Run()
}
