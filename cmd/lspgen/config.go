package main

// This file is the codegen allowlist: mgmt 8a6c4d87 asks for a CONFIGURED
// ALLOWLIST of structures/enumerations, not the whole LSP 3.18 surface — just
// what pkg/lsp uses today plus the near-term read-method surface. Every name
// below is resolved against cmd/lspgen/metaModel.json; anything the resolver
// can't handle mechanically (an "or" type, an unregistered reference, an
// unsupported base type) is a hard generation error, not a guess — see
// resolveField in generate.go.
//
// Deliberately EXCLUDED from generation (stay hand-written in pkg/lsp, with
// the reason noted here so the next reader doesn't have to rediscover it):
//
//   - WorkspaceEdit — [WorkspaceEdit.UnmarshalJSON] normalizes BOTH wire forms
//     ("changes" and "documentChanges") into one Changes map; that's
//     hand-tuned behavior, not a data shape.
//   - TextDocumentEdit, OptionalVersionedTextDocumentIdentifier — support types
//     for the above, deliberately ignoring the CreateFile/RenameFile/DeleteFile/
//     AnnotatedTextEdit/SnippetTextEdit variants WorkspaceEdit.UnmarshalJSON
//     already documents skipping.
//   - Hover — [Hover.UnmarshalJSON] normalizes the polymorphic
//     MarkupContent | MarkedString | MarkedString[] contents field into a
//     single MarkupContent; MarkedString is not modeled at all.
//   - TextDocumentContentChangeEvent — the metaModel type is
//     TextDocumentContentChangePartial | TextDocumentContentChangeWholeDocument;
//     lspbridge advertises whole-document sync only (see lifecycle.go) and
//     intentionally models just that arm.
//   - DocumentSymbolResult — invented by lspbridge to discriminate the
//     DocumentSymbol[] | SymbolInformation[] | null result of
//     textDocument/documentSymbol; it has no metaModel structure of its own.
//   - InitializeParams — the real metaModel type composes _InitializeParams
//     (~8 fields incl. deprecated rootPath, nullable rootUri, locale, trace,
//     initializationOptions) with WorkspaceFoldersInitializeParams; lspbridge
//     sends a small fixed subset (processId, rootUri, capabilities,
//     clientInfo) and isn't ready to take on the rest yet.
//   - InitializeResult.Capabilities stays json.RawMessage (see the override
//     below) rather than the full ServerCapabilities structure, which is by
//     far the single largest type in the whole metaModel and is deliberately
//     not modeled — per-server feature detection reads the raw JSON instead.
//
// None of Client's request methods (DocumentSymbol, References, Definition,
// Hover, Rename, FoldingRange, WorkspaceSymbol) or their null/polymorphic
// result-decoding helpers are in scope here at all: those are behavior, not
// data shapes, and stay hand-written regardless of what's generated.

// fieldOverride replaces the mechanically-resolved Go type for one specific
// "Structure.property" with an explicit type, and records why — every
// override exists because the mechanical rule would otherwise produce a
// breaking or spec-overreaching result for existing pkg/lsp call sites.
type fieldOverride struct {
	goType string
	reason string
}

// structConfig configures one generated struct.
type structConfig struct {
	name string // metaModel structure name; also the emitted Go type name.
	doc  string // Go doc comment for the type (one line, "Name ..." form).

	// onlyFields, when non-nil, restricts AND orders the emitted fields to
	// exactly this metaModel property-name list (drawn from the structure's
	// fully-flattened extends+own property set — see flattenProperties in
	// generate.go). When nil, every own property is emitted (extends is
	// always flattened in; mixins are never auto-included, see below), in
	// natural flatten order.
	onlyFields []string

	// fieldDoc supplies a one-line doc comment for specific fields that
	// warrant one beyond the generic auto-derived comment (new fields this
	// migration adds, and every overridden field's rationale).
	fieldDoc map[string]string

	// overrides replaces the mechanically-resolved Go type for specific
	// property names; see fieldOverride.
	overrides map[string]fieldOverride
}

// enumConfig configures one generated enumeration.
type enumConfig struct {
	name        string // metaModel enumeration name; also the emitted Go type name.
	doc         string // Go doc comment for the type.
	constDoc    string // Go doc comment for the const block (or lone const).
	constPrefix string // prefix for value const names; defaults to name if empty.
}

// Note on "extends" vs "mixins": the metaModel distinguishes single/multiple
// *inheritance* (extends — a real "is-a": SymbolInformation IS-A
// BaseSymbolInformation) from *mixins* (WorkDoneProgressParams,
// PartialResultParams — additive traits mixed into dozens of unrelated
// Params types across the whole spec for progress-token support). This
// generator always flattens extends in fully, and never auto-includes
// mixins — every *Params type in this allowlist already deliberately omits
// workDoneToken/partialResultToken (lspbridge's client never sends progress
// tokens), and dropping mixins by a single blanket rule reproduces that
// exactly, rather than requiring a per-type exclude list.

var enumConfigs = []enumConfig{
	{
		name:        "DiagnosticSeverity",
		doc:         "DiagnosticSeverity is the LSP severity enumeration.",
		constDoc:    "LSP diagnostic severities.",
		constPrefix: "Severity", // NOT "DiagnosticSeverity" — matches the pre-existing hand-written names (SeverityError etc.) that pkg/lsp callers already use.
	},
	{
		name:     "SymbolKind",
		doc:      "SymbolKind is the LSP symbol-kind enumeration (1..26).",
		constDoc: "LSP symbol kinds.",
	},
	{
		name:     "SymbolTag",
		doc:      "SymbolTag is the LSP symbol-tag enumeration.",
		constDoc: "LSP symbol tags.",
	},
	{
		name: "FoldingRangeKind",
		doc: "FoldingRangeKind categorizes a FoldingRange (e.g. so a client's \"fold\n" +
			"all comments\" command can select just that kind). Per the LSP spec it is\n" +
			"an open string enumeration — the values below are the standardized ones\n" +
			"(LSP metaModel, FoldingRangeKind), but a server may report others.",
		constDoc: "Standardized folding range kinds.",
	},
}

var structConfigs = []structConfig{
	// --- Base geometry -----------------------------------------------------
	{
		name: "Position",
		doc:  "Position is a zero-based line/character offset. Per the LSP spec character\noffsets are UTF-16 code units by default; callers that map to the structast\nDTO (1-indexed, UTF-8) convert at the boundary.",
	},
	{
		name: "Range",
		doc:  "Range is a [Start, End) span within a document.",
	},
	{
		name: "Location",
		doc:  "Location is a range within a specific document.",
	},
	{
		name: "LocationLink",
		doc:  "LocationLink is a definition/declaration result element that additionally\ncarries the origin span the link was resolved from. lspbridge's Definition\nmethod does not advertise linkSupport (see reads.go) so servers never send\nthis today; it is generated now as near-term surface for a client that does.",
		fieldDoc: map[string]string{
			"originSelectionRange": "OriginSelectionRange, if present, is the span in the source document the link was resolved from.",
			"targetUri":            "TargetURI is the document the link points to.",
			"targetRange":          "TargetRange is the full range of the target element (e.g. a whole function body).",
			"targetSelectionRange": "TargetSelectionRange is the narrower span to actually select/highlight (e.g. just the function name); always contained within TargetRange.",
		},
	},
	{
		name: "TextEdit",
		doc:  "TextEdit replaces Range with NewText.",
	},

	// --- Diagnostics ---------------------------------------------------------
	{
		name: "Diagnostic",
		doc:  "Diagnostic is a problem reported at a range.",
		// Kept to the pre-existing field set PLUS RelatedInformation (mgmt
		// 8a6c4d87's explicit "Diagnostic(+related)" instruction). Tags,
		// CodeDescription and Data are equally mechanical additions but are
		// out of scope for this pass — not requested, and each is its own
		// small allowlist entry when a consumer actually needs it.
		onlyFields: []string{"range", "severity", "code", "source", "message", "relatedInformation"},
		fieldDoc: map[string]string{
			"relatedInformation": "RelatedInformation lists related diagnostic locations, e.g. when symbol-names within a scope collide all definitions can be marked via this property (LSP metaModel: Diagnostic.relatedInformation).",
		},
		overrides: map[string]fieldOverride{
			"code": {
				goType: "any",
				reason: "Code is any because the LSP spec allows either an integer or a string code.",
			},
			"message": {
				goType: "string",
				reason: "Message stays string: LSP 3.18 widened this to string | MarkupContent behind the textDocument.diagnostic.markupMessageSupport client capability, which lspbridge does not advertise, so only the string arm is ever populated on the wire in practice.",
			},
		},
	},
	{
		name: "DiagnosticRelatedInformation",
		doc:  "DiagnosticRelatedInformation points at another location related to a\nDiagnostic (e.g. another definition site involved in a name collision).",
	},

	// --- Symbols -------------------------------------------------------------
	{
		name: "DocumentSymbol",
		doc:  "DocumentSymbol is the modern, hierarchical result element of\ntextDocument/documentSymbol: a symbol with a nesting Range, a narrower\nSelectionRange (the name), and nested Children. A server returns this shape\nonly when the client advertises hierarchicalDocumentSymbolSupport; otherwise\nit returns the flat [SymbolInformation] form.",
	},
	{
		name: "SymbolInformation",
		doc:  "SymbolInformation is the legacy, flat result element of\ntextDocument/documentSymbol (and the workspace/symbol result): a symbol with\na full [Location] rather than nested ranges. ContainerName is the enclosing\nsymbol's name, if any.",
		// Full flatten of BaseSymbolInformation (name,kind,tags,containerName)
		// + SymbolInformation's own (deprecated,location); onlyFields here is
		// just fixing emission order to match the pre-existing hand-written
		// layout, not excluding anything.
		onlyFields: []string{"name", "kind", "tags", "deprecated", "location", "containerName"},
	},
	{
		name: "PublishDiagnosticsParams",
		doc:  "PublishDiagnosticsParams is the payload of the server-to-client\ntextDocument/publishDiagnostics notification.",
	},

	// --- Folding ---------------------------------------------------------------
	{
		name: "FoldingRange",
		doc:  "FoldingRange is one foldable line span. StartCharacter/EndCharacter are\npointers because the LSP spec gives the unset case its own meaning\n(\"defaults to the length of the start/end line\") distinct from character 0;\na server that folds whole lines (as org-lsp does) leaves both nil.",
		overrides: map[string]fieldOverride{
			"startCharacter": {
				goType: "*int",
				reason: "Pointer (not a plain optional int): the LSP spec gives \"unset\" its own meaning (\"defaults to the length of the start line\") distinct from character 0, which int+omitempty cannot represent.",
			},
			"endCharacter": {
				goType: "*int",
				reason: "Pointer (not a plain optional int): the LSP spec gives \"unset\" its own meaning (\"defaults to the length of the end line\") distinct from character 0, which int+omitempty cannot represent.",
			},
		},
	},

	// --- Hover content (Hover itself stays hand-written) ----------------------
	{
		name: "MarkupContent",
		doc:  "MarkupContent is rendered hover/documentation content. Kind is \"plaintext\"\nor \"markdown\".",
		overrides: map[string]fieldOverride{
			"kind": {
				goType: "string",
				reason: "Kind stays string rather than the MarkupKind enum: pkg/lsp's decodeMarkup/decodeMarkupObject helpers (reads.go) build it from literal string constants, and existing tests compare it directly against string locals.",
			},
		},
	},

	// --- Read-method Params types ----------------------------------------------
	{
		name: "TextDocumentIdentifier",
		doc:  "TextDocumentIdentifier names a document by URI.",
	},
	{
		name: "ReferenceContext",
		doc:  "ReferenceContext scopes a references request; IncludeDeclaration adds the\nsymbol's own declaration to the returned locations.",
	},
	{
		name: "ReferenceParams",
		doc:  "ReferenceParams is the textDocument/references request payload. Position is\n0-indexed with a UTF-16 character offset.",
	},
	{
		name: "DefinitionParams",
		doc:  "DefinitionParams is the textDocument/definition request payload. Position is\n0-indexed with a UTF-16 character offset.",
	},
	{
		name: "HoverParams",
		doc:  "HoverParams is the textDocument/hover request payload. Position is 0-indexed\nwith a UTF-16 character offset.",
	},
	{
		name: "RenameParams",
		doc:  "RenameParams is the textDocument/rename request payload: rename the symbol at\nPosition (0-indexed, UTF-16) to NewName. The server computes the edit purely\nand returns a [WorkspaceEdit]; it never writes to disk.",
	},
	{
		name: "FoldingRangeParams",
		doc:  "FoldingRangeParams is the textDocument/foldingRange request payload.",
	},
	{
		name: "WorkspaceSymbolParams",
		doc:  "WorkspaceSymbolParams is the workspace/symbol request payload. Per the LSP\nspec, Query should be matched \"in a relaxed way\" (case-insensitive,\ncharacters appearing in order); an empty Query requests every symbol.",
	},
	{
		name: "DocumentSymbolParams",
		doc:  "DocumentSymbolParams is the textDocument/documentSymbol request payload.",
	},

	// --- Lifecycle / text sync -------------------------------------------------
	{
		name: "ServerInfo",
		doc:  "ServerInfo identifies the server.",
	},
	{
		name: "ClientInfo",
		doc:  "ClientInfo identifies the client to the server.",
	},
	{
		name: "TextDocumentItem",
		doc:  "TextDocumentItem is a document opened on the server.",
		overrides: map[string]fieldOverride{
			"languageId": {
				goType: "string",
				reason: "LanguageID stays string rather than the LanguageKind enum: it's an open enumeration (supportsCustomValues) and every existing call site (pyright.go, broker.go, tests) passes/receives a plain string local.",
			},
		},
	},
	{
		name: "VersionedTextDocumentIdentifier",
		doc:  "VersionedTextDocumentIdentifier identifies a document at a version.",
	},
	{
		name: "DidOpenTextDocumentParams",
		doc:  "DidOpenTextDocumentParams is the textDocument/didOpen payload.",
	},
	{
		name: "DidChangeTextDocumentParams",
		doc:  "DidChangeTextDocumentParams is the textDocument/didChange payload.",
	},
	{
		name: "InitializeResult",
		doc:  "InitializeResult is the server's initialize response. The raw capabilities\nobject is retained verbatim so per-server feature detection can inspect it\nwithout this package modeling the whole surface.",
		overrides: map[string]fieldOverride{
			"capabilities": {
				goType: "json.RawMessage",
				reason: "Capabilities stays raw JSON: ServerCapabilities is by far the largest structure in the whole metaModel and modeling it is not needed for per-server feature detection, which reads this raw object directly.",
			},
		},
	},

	// --- Client capabilities (deliberately trimmed at every level) --------------
	{
		name:       "ClientCapabilities",
		doc:        "ClientCapabilities is the advertised client capability set. Kept minimal\nand honest for P0; grown as concrete features (rename, references,\ndiagnostics pull) are added.",
		onlyFields: []string{"workspace", "textDocument"},
	},
	{
		name:       "WorkspaceClientCapabilities",
		doc:        "WorkspaceClientCapabilities is the workspace-scoped capability subset.",
		onlyFields: []string{"workspaceEdit"},
	},
	{
		name:       "WorkspaceEditClientCapabilities",
		doc:        "WorkspaceEditClientCapabilities advertises WorkspaceEdit support.",
		onlyFields: []string{"documentChanges"},
	},
	{
		name:       "TextDocumentClientCapabilities",
		doc:        "TextDocumentClientCapabilities is the document-scoped capability subset.",
		onlyFields: []string{"publishDiagnostics", "documentSymbol"},
		fieldDoc: map[string]string{
			"publishDiagnostics": "PublishDiagnostics advertises support for the server-to-client\ntextDocument/publishDiagnostics notification. Per-feature capability\nstructs continue to land here as they're wired in.",
			"documentSymbol":     "DocumentSymbol advertises textDocument/documentSymbol support; its\nHierarchicalDocumentSymbolSupport flag selects the nested DocumentSymbol\nresult shape over the flat SymbolInformation one.",
		},
	},
	{
		name:       "DocumentSymbolClientCapabilities",
		doc:        "DocumentSymbolClientCapabilities advertises the client's\ntextDocument/documentSymbol support.",
		onlyFields: []string{"hierarchicalDocumentSymbolSupport"},
		fieldDoc: map[string]string{
			"hierarchicalDocumentSymbolSupport": "HierarchicalDocumentSymbolSupport asks the server to return the nested\n[DocumentSymbol] tree rather than a flat [SymbolInformation] list.",
		},
	},
	{
		name:       "PublishDiagnosticsClientCapabilities",
		doc:        "PublishDiagnosticsClientCapabilities advertises the client's\ntextDocument/publishDiagnostics support.",
		onlyFields: []string{"relatedInformation"},
	},
}

// externalTypeKind records the reference-resolution "kind" (enum-like =
// value semantics when optional, vs struct-like = pointer semantics when
// optional) for names that resolve to a type NOT generated by this file —
// either because it's deliberately hand-written (see the exclusion list
// above) or is itself a Go builtin. Every "reference"-kind field the
// allowlisted structures reach that ISN'T itself in structConfigs/enumConfigs
// must be listed here, or generation fails with an unresolved-reference
// error (see resolveField in generate.go).
var externalTypeKinds = map[string]string{
	"TextDocumentContentChangeEvent": "struct", // hand-written in lifecycle.go; DidChangeTextDocumentParams.contentChanges references it.
}

// baseTypeGo maps a metaModel "base" kind name to the Go type pkg/lsp already
// uses for it. uinteger/integer both map to plain int (matching every
// existing hand-written field — Position.Line, SymbolKind's underlying type,
// etc. — none of which use an unsigned type).
var baseTypeGo = map[string]string{
	"string":      "string",
	"boolean":     "bool",
	"integer":     "int",
	"uinteger":    "int",
	"decimal":     "float64",
	"DocumentUri": "DocumentURI", // hand-written in types.go
}
