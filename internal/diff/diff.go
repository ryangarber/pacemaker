// Package diff implements a line-based Myers diff, range relocation through
// an edit script, and unified-diff formatting. It is language agnostic.
package diff

import (
	"fmt"
	"strings"
)

// Op is an edit operation.
type Op byte

const (
	Equal  Op = '='
	Delete Op = '-'
	Insert Op = '+'
)

// Edit is one line-level operation. A/B are 0-based indexes into the old and
// new sequences; for Insert, A is the old position the line is inserted at,
// for Delete, B is the new position.
type Edit struct {
	Op   Op
	A, B int
}

// MaxEditDistance bounds work on pathological inputs; beyond it Lines
// returns a coarse "replace everything in the middle" script.
var MaxEditDistance = 4000

// SplitLines splits text into lines without trailing newline characters.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// Lines computes an edit script transforming a into b.
func Lines(a, b []string) []Edit {
	// Intern lines so comparisons are integer comparisons.
	ids := map[string]int{}
	intern := func(xs []string) []int {
		out := make([]int, len(xs))
		for i, x := range xs {
			id, ok := ids[x]
			if !ok {
				id = len(ids)
				ids[x] = id
			}
			out[i] = id
		}
		return out
	}
	return Ints(intern(a), intern(b))
}

// Ints computes an edit script over integer sequences.
func Ints(a, b []int) []Edit {
	// Trim common prefix and suffix; most real edits are local.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var edits []Edit
	for i := 0; i < pre; i++ {
		edits = append(edits, Edit{Equal, i, i})
	}
	mid := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	for _, e := range mid {
		edits = append(edits, Edit{e.Op, e.A + pre, e.B + pre})
	}
	for i := 0; i < suf; i++ {
		edits = append(edits, Edit{Equal, len(a) - suf + i, len(b) - suf + i})
	}
	return edits
}

func myers(a, b []int) []Edit {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	maxD := n + m
	if maxD > MaxEditDistance {
		maxD = MaxEditDistance
	}
	off := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int
	found := -1
	for d := 0; d <= maxD && found < 0; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				break
			}
		}
	}
	if found < 0 {
		// Too different: delete all, insert all.
		var out []Edit
		for i := 0; i < n; i++ {
			out = append(out, Edit{Delete, i, 0})
		}
		for j := 0; j < m; j++ {
			out = append(out, Edit{Insert, n, j})
		}
		return out
	}
	// Backtrack.
	var rev []Edit
	x, y := n, m
	for d := found; d >= 0; d-- {
		vd := trace[d]
		k := x - y
		var prevK int
		if d == 0 {
			for x > 0 && y > 0 {
				x--
				y--
				rev = append(rev, Edit{Equal, x, y})
			}
			break
		}
		if k == -d || (k != d && vd[off+k-1] < vd[off+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := vd[off+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Edit{Equal, x, y})
		}
		if x == prevX {
			y--
			rev = append(rev, Edit{Insert, x, y})
		} else {
			x--
			rev = append(rev, Edit{Delete, x, y})
		}
	}
	out := make([]Edit, len(rev))
	for i := range rev {
		out[i] = rev[len(rev)-1-i]
	}
	return out
}

// LineMap maps 1-based old line numbers to 1-based new line numbers for
// lines that survived unchanged (0 = changed or deleted).
func LineMap(edits []Edit, oldLen int) []int {
	m := make([]int, oldLen+2)
	for _, e := range edits {
		if e.Op == Equal {
			m[e.A+1] = e.B + 1
		}
	}
	return m
}

// MapRange relocates an old 1-based inclusive range into the new file.
// ok is false when every line of the range was deleted.
//
// The start boundary follows the first surviving line; if the first line was
// itself changed, the range extends back to just after the previous surviving
// line so modifications at the edges stay inside the block.
func MapRange(edits []Edit, oldLen, newLen, start, end int) (ns, ne int, ok bool) {
	m := LineMap(edits, oldLen)
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	start, end = clamp(start, 1, oldLen), clamp(end, 1, oldLen)
	if m[start] > 0 {
		ns = m[start]
	} else {
		ns = 1
		for i := start - 1; i >= 1; i-- {
			if m[i] > 0 {
				ns = m[i] + 1
				break
			}
		}
	}
	if m[end] > 0 {
		ne = m[end]
	} else {
		ne = newLen
		for i := end + 1; i <= oldLen; i++ {
			if m[i] > 0 {
				ne = m[i] - 1
				break
			}
		}
	}
	if ne < ns {
		return ns, ns - 1, false
	}
	return ns, ne, true
}

// Stats counts inserted and deleted lines.
func Stats(edits []Edit) (ins, del int) {
	for _, e := range edits {
		switch e.Op {
		case Insert:
			ins++
		case Delete:
			del++
		}
	}
	return
}

// Unified renders a unified diff with the given context. Line numbers in hunk
// headers are offset by aBase/bBase (the 1-based line number of a[0]/b[0]).
func Unified(a, b []string, aName, bName string, aBase, bBase, context int) string {
	edits := Lines(a, b)
	if ins, del := Stats(edits); ins == 0 && del == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aName, bName)
	for _, h := range Hunks(edits, context) {
		as, bs := h[0].A, h[0].B
		var ac, bc int
		for _, e := range h {
			if e.Op != Insert {
				ac++
			}
			if e.Op != Delete {
				bc++
			}
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", as+aBase, ac, bs+bBase, bc)
		for _, e := range h {
			switch e.Op {
			case Equal:
				sb.WriteString(" " + a[e.A] + "\n")
			case Delete:
				sb.WriteString("-" + a[e.A] + "\n")
			case Insert:
				sb.WriteString("+" + b[e.B] + "\n")
			}
		}
	}
	return sb.String()
}

// Hunks groups edits into hunks with surrounding context.
func Hunks(edits []Edit, context int) [][]Edit {
	var hunks [][]Edit
	n := len(edits)
	i := 0
	for i < n {
		for i < n && edits[i].Op == Equal {
			i++
		}
		if i >= n {
			break
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		// Extend while changes are within 2*context of each other.
		end := i
		for end < n {
			if edits[end].Op != Equal {
				end++
				continue
			}
			j := end
			for j < n && edits[j].Op == Equal {
				j++
			}
			if j < n && j-end <= 2*context {
				end = j
				continue
			}
			end += context
			if end > n {
				end = n
			}
			break
		}
		hunks = append(hunks, edits[start:end])
		i = end
	}
	return hunks
}

// Similarity returns a 0..1 score of how many lines of a survive in b.
func Similarity(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	eq := 0
	for _, e := range Lines(a, b) {
		if e.Op == Equal {
			eq++
		}
	}
	return 2 * float64(eq) / float64(len(a)+len(b))
}
