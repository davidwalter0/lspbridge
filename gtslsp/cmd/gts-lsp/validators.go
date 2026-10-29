package main

import "github.com/davidwalter0/lspbridge/gtslsp/validate"

// validators lists the external validators run on didOpen/didSave.
//
// TODO(merge, validate): return validate.All() once that function lands.
func validators() []validate.Validator { return nil }
