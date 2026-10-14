package database

import (
	"context"
	"testing"

	"github.com/Harkole/fenrir-daemon/internal/model"
)

func TestPortAllocationAndConflicts(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	portsA := []model.ProjectPort{
		{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"},
		{HostPort: 19132, ContainerPort: 19132, Protocol: "udp"},
	}

	// Allocate to project A
	if err := db.AllocateProjectPorts(ctx, "project-a", portsA); err != nil {
		t.Fatalf("failed to allocate ports for project-a: %v", err)
	}

	// Verify ports allocated to project A
	retrievedA, err := db.GetProjectPorts(ctx, "project-a")
	if err != nil {
		t.Fatalf("failed to get ports: %v", err)
	}
	if len(retrievedA) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(retrievedA))
	}

	// Check conflicts for project-b requesting port 25565/tcp
	conflicts, err := db.CheckPortConflicts(ctx, "project-b", []model.ProjectPort{
		{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"},
		{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
	})
	if err != nil {
		t.Fatalf("failed to check conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].HostPort != 25565 || conflicts[0].AllocatedTo != "project-a" {
		t.Fatalf("unexpected conflict: %+v", conflicts[0])
	}

	// Same port but different protocol (udp 25565) should NOT conflict
	conflictsUdp, err := db.CheckPortConflicts(ctx, "project-b", []model.ProjectPort{
		{HostPort: 25565, ContainerPort: 25565, Protocol: "udp"},
	})
	if err != nil {
		t.Fatalf("failed to check conflicts: %v", err)
	}
	if len(conflictsUdp) != 0 {
		t.Fatalf("expected 0 conflicts for udp 25565, got %d", len(conflictsUdp))
	}

	// Project A checking its own ports should NOT conflict with itself
	selfConflicts, err := db.CheckPortConflicts(ctx, "project-a", portsA)
	if err != nil {
		t.Fatalf("failed to check self conflicts: %v", err)
	}
	if len(selfConflicts) != 0 {
		t.Fatalf("expected 0 conflicts when project checks its own ports, got %d", len(selfConflicts))
	}

	// Release ports for project A
	if err := db.ReleaseProjectPorts(ctx, "project-a"); err != nil {
		t.Fatalf("failed to release ports: %v", err)
	}

	// Now project B shouldn't have any conflicts
	conflictsAfterRelease, err := db.CheckPortConflicts(ctx, "project-b", []model.ProjectPort{
		{HostPort: 25565, ContainerPort: 25565, Protocol: "tcp"},
	})
	if err != nil {
		t.Fatalf("failed to check conflicts after release: %v", err)
	}
	if len(conflictsAfterRelease) != 0 {
		t.Fatalf("expected 0 conflicts after release, got %d", len(conflictsAfterRelease))
	}
}

func TestListActivePorts(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_ = db.AllocateProjectPorts(ctx, "p1", []model.ProjectPort{{HostPort: 80, ContainerPort: 80, Protocol: "tcp"}})
	_ = db.AllocateProjectPorts(ctx, "p2", []model.ProjectPort{{HostPort: 443, ContainerPort: 443, Protocol: "tcp"}})

	active, err := db.ListActivePorts(ctx)
	if err != nil {
		t.Fatalf("failed to list active ports: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active ports, got %d", len(active))
	}
	if active[0].HostPort != 80 || active[1].HostPort != 443 {
		t.Fatalf("unexpected order or ports: %+v", active)
	}
}
