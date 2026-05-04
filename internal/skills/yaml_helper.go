package skills

import "gopkg.in/yaml.v3"

func parseYAML(b []byte) (map[string]any, error) {
	var fm map[string]any
	if err := yaml.Unmarshal(b, &fm); err != nil {
		return nil, err
	}
	return fm, nil
}
