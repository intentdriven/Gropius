package main

import (
	"os/exec"
	"strings"
)

// copyToClipboard puts text on the macOS pasteboard via pbcopy, which avoids
// pulling in a cgo clipboard dependency for one menu item.
//
// The absolute path, not the name. This one needs a menu click, so it is the
// mildest of the three call sites the rule covers — but the rule is "pin it",
// not "pin it where an attacker could reach": on a Mac shared by several
// accounts a directory another account writes can sit ahead of /usr/bin on
// this one's PATH, and a bare name is in any case no proof of which tool ran.
func copyToClipboard(text string) {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(text)
	cmd.Run()
}
