package wsedit

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/davidwalter0/lspbridge/pkg/lsp"
)

func pos(line, char int) lsp.Position { return lsp.Position{Line: line, Character: char} }

func rng(sl, sc, el, ec int) lsp.Range {
	return lsp.Range{Start: pos(sl, sc), End: pos(el, ec)}
}

func TestByteOffset(t *testing.T) {
	tests := []struct {
		name string
		src  string
		pos  lsp.Position
		want int
	}{
		// --- single line, ASCII ---
		{"start of file", "hello world", pos(0, 0), 0},
		{"mid ascii line", "hello world", pos(0, 6), 6},
		{"end of ascii line", "hello world", pos(0, 11), 11},
		{"char past line end clamps", "hello", pos(0, 99), 5},

		// --- multi-line, \n ---
		{"second line start", "a\nb\nc", pos(1, 0), 2},
		{"third line start", "a\nb\nc", pos(2, 0), 4},
		{"mid third line", "abc\ndef\nghi", pos(2, 2), 10},
		{"append at EOF virtual line", "a\nb\n", pos(2, 0), 4},

		// --- \r\n line endings ---
		{"crlf second line start", "a\r\nb", pos(1, 0), 3},
		{"crlf char past text clamps before cr", "ab\r\ncd", pos(0, 9), 2},
		{"crlf end of line text", "ab\r\ncd", pos(0, 2), 2},

		// --- multibyte BMP (é = U+00E9, 2 bytes UTF-8, 1 UTF-16 unit) ---
		{"after e-acute", "héllo", pos(0, 2), 3},
		{"end of accented word", "héllo", pos(0, 5), 6},
		{"char in second line after accent", "x\nhé", pos(1, 2), 5},

		// --- CJK (U+4E16 世, 3 bytes UTF-8, 1 UTF-16 unit) ---
		{"after cjk char", "世界", pos(0, 1), 3},
		{"after two cjk chars", "世界", pos(0, 2), 6},

		// --- astral / surrogate pair (😀 = U+1F600, 4 bytes UTF-8, 2 UTF-16 units) ---
		{"before emoji", "a😀b", pos(0, 1), 1},
		{"after emoji (2 utf16 units)", "a😀b", pos(0, 3), 5},
		{"after trailing char post emoji", "a😀b", pos(0, 4), 6},
		{"mid-surrogate clamps to rune start", "a😀b", pos(0, 2), 1},
		{"two emoji then char", "😀😀x", pos(0, 4), 8},

		// --- empty file ---
		{"empty file offset 0", "", pos(0, 0), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ByteOffset([]byte(tt.src), tt.pos)
			if err != nil {
				t.Fatalf("ByteOffset(%q, %v) error: %v", tt.src, tt.pos, err)
			}
			if got != tt.want {
				t.Errorf("ByteOffset(%q, %v) = %d, want %d", tt.src, tt.pos, got, tt.want)
			}
		})
	}
}

func TestByteOffsetErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		pos  lsp.Position
	}{
		{"line beyond EOF", "a\nb", pos(5, 0)},
		{"negative line", "abc", pos(-1, 0)},
		{"negative character", "abc", pos(0, -1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ByteOffset([]byte(tt.src), tt.pos); err == nil {
				t.Errorf("ByteOffset(%q, %v) expected error, got nil", tt.src, tt.pos)
			}
		})
	}
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		name    string
		uri     lsp.DocumentURI
		want    string
		wantErr bool
	}{
		{"plain abs file uri", "file:///home/u/a.py", "/home/u/a.py", false},
		{"percent-encoded space", "file:///home/u/a%20b.py", "/home/u/a b.py", false},
		{"percent-encoded unicode", "file:///tmp/%E4%B8%96.txt", "/tmp/世.txt", false},
		{"no scheme treated as path", "/home/u/a.py", "/home/u/a.py", false},
		{"unsupported scheme", "untitled:Untitled-1", "", true},
		{"http scheme rejected", "https://example.com/x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := URIToPath(tt.uri)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("URIToPath(%q) expected error, got %q", tt.uri, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("URIToPath(%q) error: %v", tt.uri, err)
			}
			if got != tt.want {
				t.Errorf("URIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestFromWorkspaceEdit_SingleEdit(t *testing.T) {
	src := "hello world"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///f.txt": {
				{Range: rng(0, 6, 0, 11), NewText: "there"}, // replace "world"
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///f.txt": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 {
		t.Fatalf("want 1 file, got %d", len(plan.Files))
	}
	fe := plan.Files[0]
	if fe.Path != "/f.txt" {
		t.Errorf("path = %q, want /f.txt", fe.Path)
	}
	wantEdits := []Edit{{Start: 6, End: 11, NewText: "there"}}
	if !reflect.DeepEqual(fe.Edits, wantEdits) {
		t.Errorf("edits = %+v, want %+v", fe.Edits, wantEdits)
	}
	got, err := fe.Apply([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello there" {
		t.Errorf("Apply = %q, want %q", got, "hello there")
	}
}

func TestFromWorkspaceEdit_MultiEditSameFile_DescendingAndApply(t *testing.T) {
	// "foo bar baz" -> replace foo->FOO (0..3) and baz->BAZ (8..11).
	// The two edits arrive in ascending order; the plan must store them
	// descending so applying left-to-right stays offset-safe.
	src := "foo bar baz"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///s.txt": {
				{Range: rng(0, 0, 0, 3), NewText: "FOO"},
				{Range: rng(0, 8, 0, 11), NewText: "BAZ"},
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///s.txt": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	fe := plan.Files[0]
	if len(fe.Edits) != 2 || fe.Edits[0].Start != 8 || fe.Edits[1].Start != 0 {
		t.Fatalf("edits not sorted descending: %+v", fe.Edits)
	}
	got, err := fe.Apply([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FOO bar BAZ" {
		t.Errorf("Apply = %q, want %q", got, "FOO bar BAZ")
	}
}

func TestFromWorkspaceEdit_InsertionAndDeletion(t *testing.T) {
	// Pure insertion (Start==End) at offset 0 and a deletion of "cd" (2..4).
	src := "abcd"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///x": {
				{Range: rng(0, 0, 0, 0), NewText: ">>"}, // insert at start
				{Range: rng(0, 2, 0, 4), NewText: ""},   // delete "cd"
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///x": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Files[0].Apply([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != ">>ab" {
		t.Errorf("Apply = %q, want %q", got, ">>ab")
	}
}

func TestFromWorkspaceEdit_MultiLineUnicodeReplacement(t *testing.T) {
	// Replace the astral char on line 1 with ascii; verify offsets land right.
	src := "line0\n😀tail"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///u.txt": {
				{Range: rng(1, 0, 1, 2), NewText: "X"}, // 😀 is 2 UTF-16 units
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///u.txt": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	fe := plan.Files[0]
	// line1 starts at byte 6; 😀 occupies bytes 6..10.
	want := []Edit{{Start: 6, End: 10, NewText: "X"}}
	if !reflect.DeepEqual(fe.Edits, want) {
		t.Fatalf("edits = %+v, want %+v", fe.Edits, want)
	}
	got, err := fe.Apply([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "line0\nXtail" {
		t.Errorf("Apply = %q, want %q", got, "line0\nXtail")
	}
}

func TestFromWorkspaceEdit_MultiFile(t *testing.T) {
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///b.txt": {{Range: rng(0, 0, 0, 1), NewText: "B"}},
			"file:///a.txt": {{Range: rng(0, 0, 0, 1), NewText: "A"}},
		},
	}
	sources := map[lsp.DocumentURI][]byte{
		"file:///a.txt": []byte("xyz"),
		"file:///b.txt": []byte("xyz"),
	}
	plan, err := FromWorkspaceEdit(we, sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 {
		t.Fatalf("want 2 files, got %d", len(plan.Files))
	}
	// Files sorted by Path → a.txt before b.txt (deterministic).
	if plan.Files[0].Path != "/a.txt" || plan.Files[1].Path != "/b.txt" {
		t.Errorf("files not sorted by path: %q, %q", plan.Files[0].Path, plan.Files[1].Path)
	}
}

func TestFromWorkspaceEdit_MissingSource(t *testing.T) {
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///gone.txt": {{Range: rng(0, 0, 0, 1), NewText: "Z"}},
		},
	}
	if _, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{}); err == nil {
		t.Error("expected error for missing source bytes, got nil")
	}
}

func TestFromWorkspaceEdit_OverlapRejected(t *testing.T) {
	// [0,4) and [2,6) overlap → must be rejected.
	src := "abcdefgh"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///o.txt": {
				{Range: rng(0, 0, 0, 4), NewText: "X"},
				{Range: rng(0, 2, 0, 6), NewText: "Y"},
			},
		},
	}
	if _, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///o.txt": []byte(src)}); err == nil {
		t.Error("expected overlap error, got nil")
	}
}

func TestFromWorkspaceEdit_AdjacentEditsAllowed(t *testing.T) {
	// [0,2) and [2,4) touch but do not overlap → allowed.
	src := "abcd"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///adj.txt": {
				{Range: rng(0, 0, 0, 2), NewText: "AB"},
				{Range: rng(0, 2, 0, 4), NewText: "CD"},
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///adj.txt": []byte(src)})
	if err != nil {
		t.Fatalf("adjacent edits should be allowed: %v", err)
	}
	got, err := plan.Files[0].Apply([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ABCD" {
		t.Errorf("Apply = %q, want ABCD", got)
	}
}

func TestApply_OutOfRange(t *testing.T) {
	fe := FileEdit{Path: "/x", Edits: []Edit{{Start: 0, End: 100, NewText: "z"}}}
	if _, err := fe.Apply([]byte("short")); err == nil {
		t.Error("expected out-of-range error, got nil")
	}
}

func TestApply_DetectsOverlapDefensively(t *testing.T) {
	// Hand-built (bypassing FromWorkspaceEdit) descending but overlapping.
	fe := FileEdit{Path: "/x", Edits: []Edit{
		{Start: 4, End: 8, NewText: "A"},
		{Start: 2, End: 6, NewText: "B"}, // End 6 > prev.Start 4 → overlap
	}}
	if _, err := fe.Apply([]byte("abcdefghij")); err == nil {
		t.Error("expected defensive overlap error, got nil")
	}
}

func TestApply_NotDescendingRejected(t *testing.T) {
	fe := FileEdit{Path: "/x", Edits: []Edit{
		{Start: 0, End: 1, NewText: "A"},
		{Start: 4, End: 5, NewText: "B"}, // ascending → violates contract
	}}
	if _, err := fe.Apply([]byte("abcdefghij")); err == nil {
		t.Error("expected not-descending error, got nil")
	}
}

func TestApply_EmptyPlanNoEdits(t *testing.T) {
	fe := FileEdit{Path: "/x"}
	got, err := fe.Apply([]byte("unchanged"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unchanged" {
		t.Errorf("Apply with no edits = %q, want unchanged", got)
	}
}

func TestFromWorkspaceEdit_BadURI(t *testing.T) {
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"untitled:Untitled-1": {{Range: rng(0, 0, 0, 1), NewText: "Z"}},
		},
	}
	sources := map[lsp.DocumentURI][]byte{"untitled:Untitled-1": []byte("abc")}
	if _, err := FromWorkspaceEdit(we, sources); err == nil {
		t.Error("expected error for non-file URI scheme, got nil")
	}
}

func TestFromWorkspaceEdit_PositionOutOfRange(t *testing.T) {
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///r.txt": {{Range: rng(9, 0, 9, 1), NewText: "Z"}}, // line 9 beyond EOF
		},
	}
	if _, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///r.txt": []byte("a\nb")}); err == nil {
		t.Error("expected out-of-range position error, got nil")
	}
}

func TestFromWorkspaceEdit_ReversedRange(t *testing.T) {
	// Range whose end precedes its start (start char 5, end char 2).
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///rev.txt": {{Range: rng(0, 5, 0, 2), NewText: "Z"}},
		},
	}
	if _, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///rev.txt": []byte("abcdefgh")}); err == nil {
		t.Error("expected reversed-range error, got nil")
	}
}

func TestFromWorkspaceEdit_SameStartTieBreak(t *testing.T) {
	// A zero-width insertion at 5 and a replacement [5,7) share Start; the
	// deterministic tie-break (higher End first) keeps them apply-safe.
	src := "abcdefgh"
	we := lsp.WorkspaceEdit{
		Changes: map[lsp.DocumentURI][]lsp.TextEdit{
			"file:///tie.txt": {
				{Range: rng(0, 5, 0, 5), NewText: "<"},  // insert at 5
				{Range: rng(0, 5, 0, 7), NewText: "FG"}, // replace [5,7)
			},
		},
	}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///tie.txt": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	fe := plan.Files[0]
	if fe.Edits[0].End != 7 || fe.Edits[1].End != 5 {
		t.Errorf("tie-break not applied (want End 7 then 5): %+v", fe.Edits)
	}
}

func TestURIToPath_EmptyFilePath(t *testing.T) {
	if _, err := URIToPath("file://"); err == nil {
		t.Error("expected error for file URI with no path, got nil")
	}
}

// TestSchemaStamped is the mgmt 4c5b4838 proof: FromWorkspaceEdit stamps the
// SchemaV1 marker so ae's wseditingest.ParsePlan sees an explicit version.
func TestSchemaStamped(t *testing.T) {
	we := lsp.WorkspaceEdit{Changes: map[lsp.DocumentURI][]lsp.TextEdit{
		"file:///f.txt": {{Range: rng(0, 0, 0, 0), NewText: "x"}},
	}}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///f.txt": []byte("hello")})
	if err != nil {
		t.Fatalf("FromWorkspaceEdit: %v", err)
	}
	if plan.Schema != SchemaV1 {
		t.Fatalf("plan.Schema = %q, want %q", plan.Schema, SchemaV1)
	}
	if SchemaV1 != "wsedit/1" {
		t.Fatalf("SchemaV1 = %q, want wsedit/1 (must match ae wseditingest.SchemaV1)", SchemaV1)
	}
}

// TestSchemaWireShape locks the exact JSON shape ae's wseditingest.ParsePlan
// decodes: a lowercase "schema" key plus Go-default capitalized Files/Path/
// Edits/Start/End/NewText. If this shape drifts, ae's ingest breaks.
func TestSchemaWireShape(t *testing.T) {
	we := lsp.WorkspaceEdit{Changes: map[lsp.DocumentURI][]lsp.TextEdit{
		"file:///f": {{Range: rng(0, 0, 0, 0), NewText: "x"}},
	}}
	plan, err := FromWorkspaceEdit(we, map[lsp.DocumentURI][]byte{"file:///f": []byte("abc")})
	if err != nil {
		t.Fatalf("FromWorkspaceEdit: %v", err)
	}
	got, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"schema":"wsedit/1","Files":[{"Path":"/f","Edits":[{"Start":0,"End":0,"NewText":"x"}]}]}`
	if string(got) != want {
		t.Fatalf("wire shape drift:\n got: %s\nwant: %s", got, want)
	}
}

// TestSchemaOmitemptyOnRead confirms a plan produced before the marker existed
// (no schema field) still round-trips — ae tolerates its absence.
func TestSchemaOmitemptyOnRead(t *testing.T) {
	var p Plan
	if err := json.Unmarshal([]byte(`{"Files":[{"Path":"/f","Edits":[{"Start":0,"End":0,"NewText":"x"}]}]}`), &p); err != nil {
		t.Fatalf("unmarshal schemaless plan: %v", err)
	}
	if p.Schema != "" {
		t.Fatalf("schemaless plan decoded Schema = %q, want empty", p.Schema)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "/f" {
		t.Fatalf("files = %+v", p.Files)
	}
}
