package state

import (
	"bytes"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetBlock sets keys of one top-level block of box.yml (operator, notify),
// keeping the rest of the file and its comments. A missing block is
// appended; an empty value leaves its key alone.
func SetBlock(path, block string, kv ...string) error {
	var doc yaml.Node
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	root := doc.Content[0]
	var m *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == block {
			m = root.Content[i+1]
		}
	}
	if m == nil {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: block, Tag: "!!str"}, m)
	}
	set := func(k, v string) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			if m.Content[i].Value == k {
				m.Content[i+1].Value, m.Content[i+1].Tag = v, ""
				return
			}
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			set(kv[i], kv[i+1])
		}
	}
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// WithSignal is box.yml with person's signal: channel set to number (#596), in place
// or after their others; the people block keeps its comments, the loader checks the number.
func WithSignal(box []byte, person, number string) ([]byte, error) {
	var doc struct{ People yaml.Node }
	if err := yaml.Unmarshal(box, &doc); err != nil {
		return nil, err
	}
	people := &doc.People
	if people.Kind != yaml.MappingNode {
		people = &yaml.Node{Kind: yaml.MappingNode}
	}
	i := slices.IndexFunc(people.Content, func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Value == person }) // a key: the values are lists
	if i < 0 {
		i = len(people.Content)
		people.Content = append(people.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: person}, &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle})
	}
	seq := people.Content[i+1]
	if i := slices.IndexFunc(seq.Content, func(n *yaml.Node) bool { return strings.HasPrefix(n.Value, "signal:") }); i >= 0 {
		seq.Content[i].Value = "signal:" + number
	} else {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "signal:" + number, Style: yaml.DoubleQuotedStyle})
	}
	return WithBlock(box, "people", people)
}

// WithBlock is box.yml with its top-level block key set to v (appended when
// missing; laib zuhause, laib person signal); every other line stays as it is.
func WithBlock(box []byte, key string, v any) ([]byte, error) {
	var block bytes.Buffer
	enc := yaml.NewEncoder(&block)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]any{key: v}); err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(box), "\n"), "\n")
	start, end := len(lines), len(lines)
	for i, l := range lines {
		if strings.HasPrefix(l, key+":") {
			start, end = i, i+1
			for end < len(lines) && strings.HasPrefix(lines[end], " ") {
				end++
			}
			break
		}
	}
	head := strings.Join(lines[:start], "\n")
	if head != "" {
		head += "\n"
	}
	out := head + block.String() + strings.Join(lines[end:], "\n")
	return []byte(strings.TrimSuffix(out, "\n") + "\n"), nil
}
