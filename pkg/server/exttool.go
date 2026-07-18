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
//
// cmd is the name LookPath was asked to resolve (what actually appears on
// PATH); exttoolName is the tool name exttool's manifest tracks. These are
// the same string for most presets (e.g. "rust-analyzer" resolves and
// installs under one identical name) — callers pass cmd for both in that
// case. They differ when the resolved binary's name isn't exttool's
// package-derived tool name: the Angular Language Server's binary is
// "ngserver" (see AngularLanguageServerSpec) but exttool tracks it as
// "angular-language-server", the npm package's own slug.
func missingBinaryError(cmd, exttoolName string, lookPathErr error) error {
	return fmt.Errorf("server: %s not found on PATH (%w) — install it, or run `exttool install --tool %s --confirm` if exttool is set up on this host", cmd, lookPathErr, exttoolName)
}
