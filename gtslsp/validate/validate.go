// Package validate runs external configuration validators (yamllint,
// kubeconform, check-jsonschema, helm, taplo, hadolint) and maps their
// findings onto LSP diagnostics.
package validate

import (
	"context"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

// Document is the input a Validator sees: the absolute path, the gts language
// name that claimed it, and its current (possibly unsaved) content.
type Document struct {
	Path     string
	Language string
	Text     []byte
}

// Validator checks one kind of document with an external tool.
type Validator interface {
	// Name is the tool's name, used as the diagnostic source.
	Name() string
	// Applies reports whether this validator handles doc.
	Applies(doc Document) bool
	// Available reports whether the tool is installed; when it is not, the
	// returned hint names the install command.
	Available() (ok bool, installHint string)
	// Validate runs the tool and returns diagnostics positioned in doc.
	Validate(ctx context.Context, doc Document) ([]lsp.Diagnostic, error)
}
