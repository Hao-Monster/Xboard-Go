// Command clean-web-root creates a whiteout layer for inherited frontend assets.
// It is copied into the candidate image only for this build step and removes itself.
package main

import (
	"fmt"
	"os"
)

const (
	webRoot = "/srv/xboard/web"
	self    = "/tmp/clean-web-root"
)

func main() {
	if err := os.RemoveAll(webRoot); err != nil {
		fmt.Fprintf(os.Stderr, "remove inherited web root: %v\n", err)
		os.Exit(1)
	}
	if err := os.Remove(self); err != nil {
		fmt.Fprintf(os.Stderr, "remove cleanup helper: %v\n", err)
		os.Exit(1)
	}
}
