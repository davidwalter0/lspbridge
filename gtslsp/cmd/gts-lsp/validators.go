package main

import "github.com/davidwalter0/lspbridge/gtslsp/validate"

// validators lists the external validators run on didOpen/didSave.
func validators() []validate.Validator { return validate.All() }
