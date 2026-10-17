package compose

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// SanitizeContainerName converts a given name into a valid Docker container name:
// 1. Strips all whitespace characters.
// 2. Strips all characters not allowed in container names (only [a-zA-Z0-9_.-] are permitted).
// 3. Strips any leading non-alphanumeric characters (container names must start with [a-zA-Z0-9]).
func SanitizeContainerName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsSpace(r) {
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '.' || r == '-' {
			sb.WriteRune(r)
		}
	}
	res := sb.String()
	// Docker container names must start with an alphanumeric character
	res = strings.TrimLeft(res, "._-")
	return res
}

// ApplyContainerName parses raw compose YAML and sets or overrides the container_name
// of the target service in the compose file to match the sanitized name.
func ApplyContainerName(rawYAML, name string) (string, error) {
	if strings.TrimSpace(rawYAML) == "" {
		return rawYAML, nil
	}

	sanitized := SanitizeContainerName(name)
	if sanitized == "" {
		return "", fmt.Errorf("invalid name %q: cannot produce a valid container name", name)
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(rawYAML), &root); err != nil {
		return "", fmt.Errorf("failed to parse compose YAML: %w", err)
	}

	if len(root.Content) == 0 {
		return rawYAML, nil
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return rawYAML, nil
	}

	var servicesNode *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "services" {
			servicesNode = doc.Content[i+1]
			break
		}
	}

	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode || len(servicesNode.Content) == 0 {
		return rawYAML, nil
	}

	numServices := len(servicesNode.Content) / 2

	if numServices == 1 {
		serviceValNode := servicesNode.Content[1]
		if serviceValNode.Kind == yaml.MappingNode {
			setOrOverrideContainerName(serviceValNode, sanitized)
		}
	} else {
		// Check if any service name matches name or sanitized
		var matchedServiceVal *yaml.Node
		var serviceWithContainerName *yaml.Node
		containerNameCount := 0

		for i := 0; i < len(servicesNode.Content); i += 2 {
			svcKey := servicesNode.Content[i].Value
			svcVal := servicesNode.Content[i+1]
			if svcVal.Kind != yaml.MappingNode {
				continue
			}

			if strings.EqualFold(svcKey, name) || strings.EqualFold(svcKey, sanitized) {
				matchedServiceVal = svcVal
				break
			}

			if hasContainerName(svcVal) {
				serviceWithContainerName = svcVal
				containerNameCount++
			}
		}

		if matchedServiceVal != nil {
			setOrOverrideContainerName(matchedServiceVal, sanitized)
		} else if containerNameCount == 1 {
			setOrOverrideContainerName(serviceWithContainerName, sanitized)
		} else {
			// Default to primary (first) service
			firstSvcVal := servicesNode.Content[1]
			if firstSvcVal.Kind == yaml.MappingNode {
				setOrOverrideContainerName(firstSvcVal, sanitized)
			}
			// If other services have container_name, ensure they don't collide
			for i := 0; i < len(servicesNode.Content); i += 2 {
				svcKey := servicesNode.Content[i].Value
				svcVal := servicesNode.Content[i+1]
				if svcVal == firstSvcVal || svcVal.Kind != yaml.MappingNode {
					continue
				}
				if hasContainerName(svcVal) {
					setOrOverrideContainerName(svcVal, fmt.Sprintf("%s-%s", sanitized, SanitizeContainerName(svcKey)))
				}
			}
		}
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return "", fmt.Errorf("failed to encode modified compose YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize YAML encoder: %w", err)
	}

	return buf.String(), nil
}

func hasContainerName(serviceMap *yaml.Node) bool {
	for j := 0; j < len(serviceMap.Content); j += 2 {
		if serviceMap.Content[j].Value == "container_name" {
			return true
		}
	}
	return false
}

func setOrOverrideContainerName(serviceMap *yaml.Node, containerName string) {
	for j := 0; j < len(serviceMap.Content); j += 2 {
		if serviceMap.Content[j].Value == "container_name" {
			serviceMap.Content[j+1].Value = containerName
			serviceMap.Content[j+1].Tag = "!!str"
			serviceMap.Content[j+1].Kind = yaml.ScalarNode
			return
		}
	}

	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: "container_name",
	}
	valNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: containerName,
	}

	// Insert container_name at the top of the service mapping
	serviceMap.Content = append([]*yaml.Node{keyNode, valNode}, serviceMap.Content...)
}
