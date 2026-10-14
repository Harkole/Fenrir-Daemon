package compose

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Harkole/fenrir-daemon/internal/model"
	"gopkg.in/yaml.v3"
)

// ParseComposePorts parses a docker compose YAML content and extracts all host-to-container port mappings.
func ParseComposePorts(rawYAML string) ([]model.ProjectPort, error) {
	if strings.TrimSpace(rawYAML) == "" {
		return nil, nil
	}

	var parsed struct {
		Services map[string]struct {
			Ports []any `yaml:"ports"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal([]byte(rawYAML), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse compose YAML: %w", err)
	}

	var results []model.ProjectPort
	seen := make(map[string]bool)

	for _, service := range parsed.Services {
		for _, portEntry := range service.Ports {
			ports, err := parsePortEntry(portEntry)
			if err != nil {
				return nil, err
			}
			for _, p := range ports {
				key := fmt.Sprintf("%d/%s", p.HostPort, strings.ToLower(p.Protocol))
				if !seen[key] {
					seen[key] = true
					results = append(results, p)
				}
			}
		}
	}

	return results, nil
}

func parsePortEntry(entry any) ([]model.ProjectPort, error) {
	switch v := entry.(type) {
	case int:
		// Target container port only (random host port), no static host binding
		return nil, nil
	case string:
		return parseShortPortString(v)
	case map[string]any:
		return parseLongPortMap(v)
	default:
		return nil, nil
	}
}

func parseShortPortString(s string) ([]model.ProjectPort, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	protocol := "tcp"
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		protocol = strings.ToLower(s[idx+1:])
		s = s[:idx]
	}
	if protocol == "" {
		protocol = "tcp"
	}

	parts := strings.Split(s, ":")
	var hostStr, containerStr string

	switch len(parts) {
	case 1:
		// Only container port specified, no static host port mapping
		return nil, nil
	case 2:
		// host:container
		hostStr = parts[0]
		containerStr = parts[1]
	case 3:
		// ip:host:container
		hostStr = parts[1]
		containerStr = parts[2]
	default:
		return nil, fmt.Errorf("invalid port specification: %q", s)
	}

	return expandPortRange(hostStr, containerStr, protocol)
}

func parseLongPortMap(m map[string]any) ([]model.ProjectPort, error) {
	publishedVal, ok := m["published"]
	if !ok || publishedVal == nil {
		// No static published host port
		return nil, nil
	}

	targetVal, ok := m["target"]
	if !ok || targetVal == nil {
		return nil, fmt.Errorf("long syntax port mapping missing 'target' container port")
	}

	protocol := "tcp"
	if protoVal, ok := m["protocol"]; ok && protoVal != nil {
		if pStr, ok := protoVal.(string); ok && strings.TrimSpace(pStr) != "" {
			protocol = strings.ToLower(strings.TrimSpace(pStr))
		}
	}

	hostStr := fmt.Sprintf("%v", publishedVal)
	containerStr := fmt.Sprintf("%v", targetVal)

	return expandPortRange(hostStr, containerStr, protocol)
}

func expandPortRange(hostStr, containerStr, protocol string) ([]model.ProjectPort, error) {
	hostStart, hostEnd, err := parsePortOrRange(hostStr)
	if err != nil {
		return nil, fmt.Errorf("invalid host port %q: %w", hostStr, err)
	}

	contStart, contEnd, err := parsePortOrRange(containerStr)
	if err != nil {
		return nil, fmt.Errorf("invalid container port %q: %w", containerStr, err)
	}

	hostCount := hostEnd - hostStart + 1
	contCount := contEnd - contStart + 1

	if hostCount != contCount && contCount != 1 {
		return nil, fmt.Errorf("mismatched port range length: host has %d ports, container has %d", hostCount, contCount)
	}

	var ports []model.ProjectPort
	for i := 0; i < hostCount; i++ {
		hPort := hostStart + i
		cPort := contStart
		if contCount > 1 {
			cPort = contStart + i
		}

		ports = append(ports, model.ProjectPort{
			HostPort:      hPort,
			ContainerPort: cPort,
			Protocol:      protocol,
		})
	}

	return ports, nil
}

func parsePortOrRange(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, fmt.Errorf("empty port string")
	}

	if idx := strings.Index(s, "-"); idx != -1 {
		startStr := strings.TrimSpace(s[:idx])
		endStr := strings.TrimSpace(s[idx+1:])

		start, err := strconv.Atoi(startStr)
		if err != nil || start <= 0 || start > 65535 {
			return 0, 0, fmt.Errorf("invalid range start port %q", startStr)
		}

		end, err := strconv.Atoi(endStr)
		if err != nil || end <= 0 || end > 65535 {
			return 0, 0, fmt.Errorf("invalid range end port %q", endStr)
		}

		if start > end {
			return 0, 0, fmt.Errorf("range start %d cannot be greater than end %d", start, end)
		}

		return start, end, nil
	}

	port, err := strconv.Atoi(s)
	if err != nil || port <= 0 || port > 65535 {
		return 0, 0, fmt.Errorf("invalid port number %q", s)
	}

	return port, port, nil
}
