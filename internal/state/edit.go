package state

import (
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetBlock sets keys of one top-level block of a YAML file (box.yml operator,
// notify; policy.yml skills) as WithPath does; an empty value leaves its key alone.
func SetBlock(path, block string, kv ...string) error {
	b, err := os.ReadFile(path)
	for i := 0; err == nil && i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			b, err = WithPath(b, filepath.Base(path), block+"."+kv[i], &yaml.Node{Kind: yaml.ScalarNode, Value: kv[i+1]})
		}
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// WithPath is a YAML file with the value at a dotted path set to v, or removed
// when v is nil (#598): the one edit behind laib propose --path and --set, laib
// zuhause and laib person signal. A segment on a list names its item
// "<segment>:<value>" (people.mama.signal); in box.yml a missing node takes the
// shape its schema gives it, and a key the schema lacks is refused. Only the
// top-level block the path is in is rewritten, with its comments; every other
// line stays as it is. The state loader checks the result.
func WithPath(b []byte, file, at string, v *yaml.Node) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	segs := strings.Split(at, ".")
	top := &yaml.Node{Kind: yaml.MappingNode}
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		root := doc.Content[0]
		if i := keyAt(root, segs[0]); i >= 0 {
			k := *root.Content[i]
			k.HeadComment = "" // it stays on its own line above the block
			top.Content = []*yaml.Node{&k, root.Content[i+1]}
		}
	}
	n, t := top, reflect.TypeFor[Box]()
	for i, s := range segs {
		if t = child(t, s); t == nil && file == "box.yml" {
			return nil, fmt.Errorf("box.yml kennt kein %s", strings.Join(segs[:i+1], "."))
		}
		last := i == len(segs)-1
		if n.Kind == yaml.SequenceNode { // a list of "<kind>:<value>", like a person's channels
			j := slices.IndexFunc(n.Content, func(c *yaml.Node) bool { return strings.HasPrefix(c.Value, s+":") })
			switch {
			case !last || v != nil && v.Kind != yaml.ScalarNode:
				return nil, fmt.Errorf("%s: %s ist ein Eintrag einer Liste, sein Wert eine Zeile", at, s)
			case v == nil && j >= 0:
				n.Content = slices.Delete(n.Content, j, j+1)
			case v != nil && j >= 0:
				n.Content[j].Value = s + ":" + v.Value
			case v != nil:
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s + ":" + v.Value, Style: yaml.DoubleQuotedStyle})
			}
			break
		}
		if n.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: %s ist kein Block", at, strings.Join(segs[:i], "."))
		}
		j := keyAt(n, s)
		switch {
		case last && v == nil && j >= 0:
			n.Content = slices.Delete(n.Content, j, j+2)
		case last && v != nil && j >= 0:
			v.LineComment = cmp.Or(v.LineComment, n.Content[j+1].LineComment)
			n.Content[j+1] = v
		case last && v != nil:
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s}, v)
		case !last && j < 0:
			c := &yaml.Node{Kind: yaml.MappingNode}
			if t != nil && t.Kind() == reflect.Slice {
				c = &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s}, c)
			n = c
		case !last:
			n = n.Content[j+1]
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	start, end, indent := len(lines), len(lines), 2
	for i, l := range lines {
		if strings.HasPrefix(l, segs[0]+":") {
			start, end = i, i+1
			for end < len(lines) && strings.HasPrefix(lines[end], " ") {
				end++
			}
			if end > start+1 { // the block keeps its indent: 4 where yaml.Marshal once wrote it
				indent = len(lines[start+1]) - len(strings.TrimLeft(lines[start+1], " "))
			}
			break
		}
	}
	var block bytes.Buffer
	if len(top.Content) > 0 {
		enc := yaml.NewEncoder(&block)
		enc.SetIndent(indent)
		if err := enc.Encode(top); err != nil {
			return nil, err
		}
	}
	head := strings.Join(lines[:start], "\n")
	if head != "" {
		head += "\n"
	}
	out := head + block.String() + strings.Join(lines[end:], "\n")
	return []byte(strings.TrimSuffix(out, "\n") + "\n"), nil
}

// keyAt is the index of key s in mapping m, or -1.
func keyAt(m *yaml.Node, s string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == s {
			return i
		}
	}
	return -1
}

// child is the type box.yml's schema has at key s of t, nil when it has none.
func child(t reflect.Type, s string) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == nil:
	case t.Kind() == reflect.Map || t.Kind() == reflect.Slice:
		return t.Elem()
	case t.Kind() == reflect.Struct:
		for i := range t.NumField() {
			if f := t.Field(i); strings.Split(f.Tag.Get("yaml"), ",")[0] == s {
				return f.Type
			}
		}
	}
	return nil
}
