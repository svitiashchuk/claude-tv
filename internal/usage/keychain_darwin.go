package usage

import (
	"os"
	"os/exec"
)

// readKeychain returns the "Claude Code-credentials" item, where Claude Code keeps its login on
// macOS. The first read may make macOS ask whether claude-tv can use the item.
func readKeychain() ([]byte, error) {
	args := []string{"find-generic-password", "-s", "Claude Code-credentials", "-w"}
	if u := os.Getenv("USER"); u != "" {
		if out, err := exec.Command("/usr/bin/security", append(args, "-a", u)...).Output(); err == nil {
			return out, nil
		}
	}
	return exec.Command("/usr/bin/security", args...).Output()
}
