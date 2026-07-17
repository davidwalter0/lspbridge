package server

import "os/exec"

// PyrightCommand is the pyright language-server binary name resolved via
// PATH. Pyright ships it as "pyright-langserver" (installed via
// `npm install -g pyright` or `pip install pyright`, which vendors the same
// langserver).
const PyrightCommand = "pyright-langserver"

// PyrightSpec returns the [Spec] to launch pyright's language server over
// stdio, rooted at dir (typically the Python project root). It resolves
// PyrightCommand via exec.LookPath so callers get a clear "executable file
// not found in $PATH" error up front when pyright isn't installed, rather
// than a generic failure out of Launch.
func PyrightSpec(dir string) (Spec, error) {
	path, err := exec.LookPath(PyrightCommand)
	if err != nil {
		return Spec{}, err
	}
	return Spec{
		Command: path,
		Args:    []string{"--stdio"},
		Dir:     dir,
	}, nil
}
