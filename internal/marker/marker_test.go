package marker

import "testing"

func TestScan(t *testing.T) {
	src := `x
// @pace:begin a.one up:lib/a.dart#L1-L5 note="hello world" tags=x,y
line
  /* @pace:begin inner ref=lib/b.dart#Foo.bar ref=lib/c.dart */
  inner line
  /* @pace:end inner */
// @pace:end
<!-- @pace:skip ref=lib/legacy.dart reason="not needed" -->
# @pace:begin unclosed lib/z
`
	s := NewScanner("")
	blocks, probs := s.Scan("f.txt", []byte(src))
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks: %+v", len(blocks), blocks)
	}
	byID := map[string]Block{}
	for _, b := range blocks {
		byID[b.ID] = b
	}
	a := byID["a.one"]
	if a.Start != 3 || a.End != 6 || a.Note != "hello world" || len(a.Tags) != 2 || a.Refs[0].Ref.Target != "up" {
		t.Errorf("bad outer block: %+v", a)
	}
	in := byID["inner"]
	if in.Start != 5 || in.End != 5 || len(in.Refs) != 2 || in.Refs[0].Ref.Symbol != "Foo.bar" {
		t.Errorf("bad inner block: %+v", in)
	}
	sk := byID["skip:lib/legacy.dart"]
	if sk.Kind != KindSkip || sk.Note != "not needed" {
		t.Errorf("bad skip block: %+v", sk)
	}
	if len(probs) != 1 || probs[0].Line != 9 {
		t.Errorf("expected unclosed problem on line 9, got %+v", probs)
	}
}

func TestScanTodos(t *testing.T) {
	src := `// @pace:file lib/models.dart
// @pace:todo: port the "fast path" ref=lib/a.dart#Foo.fast owner=ana prio=high tags=perf,api
x
// @pace:begin blk lib/a.dart#L1-L5
  // @pace:todo handle retries
  # @pace:note "we keep v1 error codes"
// @pace:end
/* @pace:todo ref=lib/b.dart block=other */
// @pace:todo
// @pace:todo owner=bob
`
	s := NewScanner("")
	blocks, todos, probs := s.ScanAll("f.ts", []byte(src))
	if len(blocks) != 2 {
		t.Fatalf("blocks: %+v", blocks)
	}
	var file Block
	for _, b := range blocks {
		if b.Kind == KindFile {
			file = b
		}
	}
	if file.ID != "f.ts" || len(file.Refs) != 1 || file.Refs[0].Ref.Path != "lib/models.dart" {
		t.Errorf("@pace:file <ref> should take the lone argument as the ref: %+v", file)
	}
	if len(todos) != 4 {
		t.Fatalf("want 4 todos, got %d: %+v", len(todos), todos)
	}
	a := todos[0]
	if a.Kind != NoteTodo || a.Text != `port the "fast path"` || a.Owner != "ana" || a.Priority != "high" ||
		len(a.Tags) != 2 || len(a.Refs) != 1 || a.Refs[0].Ref.Symbol != "Foo.fast" || a.Block != "f.ts" || a.Line != 2 {
		t.Errorf("bad first todo: %+v", a)
	}
	if b := todos[1]; b.Text != "handle retries" || b.Block != "blk" {
		t.Errorf("todo inside a block should attach to it: %+v", b)
	}
	if n := todos[2]; n.Kind != NoteNote || n.Text != "we keep v1 error codes" || n.Block != "blk" {
		t.Errorf("bad note: %+v", n)
	}
	if r := todos[3]; r.Text != "" || len(r.Refs) != 1 || r.Block != "other" || !r.Explicit {
		t.Errorf("ref-only todo with block=: %+v", r)
	}
	if len(probs) != 2 || probs[0].Line != 9 || probs[1].Line != 10 {
		t.Errorf("empty todos should be problems: %+v", probs)
	}
}
