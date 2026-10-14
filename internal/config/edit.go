package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// SetValues sets settings in a config file's text and returns the new text.
// Keys are "table.key" for the settings of [detect], [forward], [ssh] and
// [ui]; values are booleans, strings, numbers or lists of them, as JSON
// decodes them. Comments and the rest of the file stay as they were. The
// result is not validated: Parse it before saving.
func SetValues(data []byte, values map[string]any) ([]byte, error) {
	known, err := knownKeys()
	if err != nil {
		return nil, err
	}
	keys := sortedKeys(values)
	for _, k := range keys {
		if !known[k] {
			return nil, fmt.Errorf("%s is not a setting the form can change; edit config.toml instead", k)
		}
	}
	out := slices.Clone(data)
	for _, k := range keys {
		text, err := tomlValue(values[k])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out = setOne(out, strings.Split(k, "."), text)
	}
	return out, nil
}

// knownKeys lists the "table.key" settings, from the defaults.
func knownKeys() (map[string]bool, error) {
	raw, err := toml.Marshal(defaultRaw())
	if err != nil {
		return nil, fmt.Errorf("list the settings: %w", err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("list the settings: %w", err)
	}
	known := map[string]bool{}
	for _, table := range []string{"detect", "forward", "ssh", "ui"} {
		t, _ := doc[table].(map[string]any)
		for k := range t {
			known[table+"."+k] = true
		}
	}
	return known, nil
}

// tomlValue writes v as a TOML value. JSON numbers arrive as float64; whole
// ones become integers. Strings use JSON's escapes, which TOML basic
// strings share.
func tomlValue(v any) (string, error) {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x), nil
	case string:
		b, err := json.Marshal(x)
		return string(b), err
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return "", fmt.Errorf("%v is not a whole number", x)
		}
		return strconv.FormatInt(int64(x), 10), nil
	case []any:
		items := make([]string, 0, len(x))
		for _, item := range x {
			if _, nested := item.([]any); nested {
				return "", errors.New("lists inside lists are not settings")
			}
			s, err := tomlValue(item)
			if err != nil {
				return "", err
			}
			items = append(items, s)
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	}
	return "", fmt.Errorf("unsupported value %v", v)
}

// setOne sets path (table, key) to the TOML text value: in place when the
// key is there, after the table's header when only the table is, and in a
// new table at the end otherwise.
func setOne(data []byte, path []string, value string) []byte {
	var p unstable.Parser
	p.Reset(data)
	var table []string
	headerEnd := -1
	for p.NextExpression() {
		e := p.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			parts, first := keyParts(e.Key())
			table = parts
			if slices.Equal(parts, path[:1]) && first != nil {
				off := p.Shape(first.Raw).End.Offset
				if nl := bytes.IndexByte(data[off:], '\n'); nl >= 0 {
					headerEnd = off + nl
				} else {
					headerEnd = len(data)
				}
			}
		case unstable.KeyValue:
			parts, _ := keyParts(e.Key())
			if !slices.Equal(append(slices.Clone(table), parts...), path) {
				continue
			}
			start, end := valueSpan(&p, data, e)
			return slices.Concat(data[:start], []byte(value), data[end:])
		}
	}
	line := path[1] + " = " + value
	if headerEnd >= 0 {
		return slices.Concat(data[:headerEnd], []byte("\n"+line), data[headerEnd:])
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	if len(data) > 0 {
		data = append(data, '\n')
	}
	return append(data, []byte("["+path[0]+"]\n"+line+"\n")...)
}

// valueSpan is where kv's value starts and ends in data. The parser keeps
// no range for an array's brackets, so an array is found by scanning from
// the "=" after the key to the matching bracket.
func valueSpan(p *unstable.Parser, data []byte, kv *unstable.Node) (start, end int) {
	v := kv.Value()
	if v.Kind != unstable.Array {
		s := p.Shape(v.Raw)
		return s.Start.Offset, s.End.Offset
	}
	var last *unstable.Node
	for it := kv.Key(); it.Next(); {
		last = it.Node()
	}
	from := p.Shape(last.Raw).End.Offset
	start = from + bytes.IndexByte(data[from:], '[')
	depth := 0
	var quote byte
	for i := start; i < len(data); i++ {
		c := data[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			if nl := bytes.IndexByte(data[i:], '\n'); nl >= 0 {
				i += nl
			}
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return start, i + 1
			}
		}
	}
	return start, len(data)
}
