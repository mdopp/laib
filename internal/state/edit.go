package state

import (
	"os"

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
