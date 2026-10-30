package plan

import (
	"fmt"
	"strings"
)

// UnifiedDiff returns a unified diff of a and b with 3 lines of context.
func UnifiedDiff(name string, a, b []byte) string {
	x := splitLines(string(a))
	y := splitLines(string(b))
	ops := diffOps(x, y)
	const ctx = 3
	var out strings.Builder
	_, _ = fmt.Fprintf(&out, "--- %s\n+++ %s\n", label(name, a), label(name, b))
	var changed []int
	for i, o := range ops {
		if o.kind != ' ' {
			changed = append(changed, i)
		}
	}
	for g := 0; g < len(changed); {
		h := g
		for h+1 < len(changed) && changed[h+1]-changed[h] <= 2*ctx+1 {
			h++
		}
		start := max(changed[g]-ctx, 0)
		end := min(changed[h]+ctx+1, len(ops))
		aStart, bStart, aLen, bLen := hunkRange(ops, start, end)
		_, _ = fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", aStart, aLen, bStart, bLen)
		for _, o := range ops[start:end] {
			out.WriteByte(o.kind)
			out.WriteString(o.text)
			out.WriteByte('\n')
		}
		g = h + 1
	}
	return out.String()
}

func label(name string, data []byte) string {
	if data == nil {
		return "/dev/null"
	}
	return name
}

type op struct {
	kind byte
	text string
	a, b int // 1-based line numbers before the op in a and b
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func diffOps(x, y []string) []op {
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			ops = append(ops, op{' ', x[i], i + 1, j + 1})
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			ops = append(ops, op{'-', x[i], i + 1, j + 1})
			i++
		default:
			ops = append(ops, op{'+', y[j], i + 1, j + 1})
			j++
		}
	}
	return ops
}

func hunkRange(ops []op, start, end int) (aStart, bStart, aLen, bLen int) {
	aStart, bStart = ops[start].a, ops[start].b
	for _, o := range ops[start:end] {
		switch o.kind {
		case ' ':
			aLen++
			bLen++
		case '-':
			aLen++
		case '+':
			bLen++
		}
	}
	if aLen == 0 {
		aStart--
	}
	if bLen == 0 {
		bStart--
	}
	return
}
