package config

import (
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// keyIndex maps each key path in a document to where its value (or, for
// tables and arrays, its key) starts, so validation problems can name a
// line and column. It uses go-toml's unstable parser; the pinned version
// and index_test.go guard against API changes.
type keyIndex map[string]pos

type pos struct{ line, col int }

func indexKey(path []string) string { return strings.Join(path, "\x00") }

// buildIndex indexes data. A syntax error stops it early, which is fine:
// the decoder reports syntax errors itself.
func buildIndex(data []byte) keyIndex {
	idx := keyIndex{}
	var p unstable.Parser
	p.Reset(data)
	var table []string
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			parts, first := keyParts(e.Key())
			table = parts
			if first != nil {
				idx.set(&p, table, first.Raw)
			}
		case unstable.KeyValue:
			idx.keyValue(&p, table, e)
		}
	}
	return idx
}

func keyParts(it unstable.Iterator) (parts []string, first *unstable.Node) {
	for it.Next() {
		n := it.Node()
		if first == nil {
			first = n
		}
		parts = append(parts, string(n.Data))
	}
	return parts, first
}

func (idx keyIndex) keyValue(p *unstable.Parser, prefix []string, kv *unstable.Node) {
	parts, first := keyParts(kv.Key())
	path := append(slices.Clone(prefix), parts...)
	v := kv.Value()
	switch {
	case v.Raw.Length > 0 && v.Kind != unstable.InlineTable:
		idx.set(p, path, v.Raw)
	case first != nil:
		idx.set(p, path, first.Raw)
	}
	if v.Kind == unstable.InlineTable {
		children := v.Children()
		for children.Next() {
			if c := children.Node(); c.Kind == unstable.KeyValue {
				idx.keyValue(p, path, c)
			}
		}
	}
}

func (idx keyIndex) set(p *unstable.Parser, path []string, r unstable.Range) {
	s := p.Shape(r).Start
	idx[indexKey(path)] = pos{s.Line, s.Column}
}

// at returns the position of path, or of its nearest indexed parent.
func (idx keyIndex) at(path ...string) (line, col int) {
	for n := len(path); n > 0; n-- {
		if q, ok := idx[indexKey(path[:n])]; ok {
			return q.line, q.col
		}
	}
	return 0, 0
}
