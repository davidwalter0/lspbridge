package server

import "fmt"

// missingBinaryError wraps a LookPath failure for a non-Go language-server
// dependency with the remediation command this ecosystem's external-tool
// installer, exttool (github.com/davidwalter0/exttool), provides: `exttool
// install --tool NAME --confirm`. It only *names* that command; it does not
// call exttool in any way (no import, no shell-out), so it carries no
// dependency and cannot itself drift out of sync with exttool's pinned
// versions — the version number, if any, is exttool's manifest's job to
// report, not this package's to duplicate.
func missingBinaryError(cmd string, lookPathErr error) error {
	return fmt.Errorf("server: %s not found on PATH (%w) — install it, or run `exttool install --tool %s --confirm` if exttool is set up on this host", cmd, lookPathErr, cmd)
}
