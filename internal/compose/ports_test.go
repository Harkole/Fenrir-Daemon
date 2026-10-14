package compose

import (
	"testing"
)

func TestParseComposePorts(t *testing.T) {
	yamlContent := `
services:
  mc:
    image: itzg/minecraft-server
    ports:
      - "25565:25565"
      - "19132:19132/udp"
      - "127.0.0.1:8080:80/tcp"
      - 9999
  web:
    image: nginx
    ports:
      - target: 80
        published: 8000
        protocol: tcp
      - "8100-8102:8000-8002"
`

	ports, err := ParseComposePorts(yamlContent)
	if err != nil {
		t.Fatalf("unexpected error parsing compose ports: %v", err)
	}

	expectedCount := 7 // 25565/tcp, 19132/udp, 8080/tcp, 8000/tcp, 8100, 8101, 8102 => 7 total
	if len(ports) != expectedCount {
		t.Fatalf("expected %d ports parsed, got %d", expectedCount, len(ports))
	}
}

func TestParseComposePortsEmpty(t *testing.T) {
	ports, err := ParseComposePorts("")
	if err != nil {
		t.Fatalf("expected nil error on empty yaml, got %v", err)
	}
	if len(ports) != 0 {
		t.Fatalf("expected 0 ports, got %d", len(ports))
	}
}

func TestParseComposePortsInvalid(t *testing.T) {
	yamlContent := `
services:
  app:
    ports:
      - "invalid:port:spec:extra"
`
	_, err := ParseComposePorts(yamlContent)
	if err == nil {
		t.Fatalf("expected error for invalid port specification")
	}
}
