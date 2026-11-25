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
