package model

import "time"

// Project represents a registered Docker Compose stack directory.
type Project struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	DirectoryPath string    `json:"directoryPath"`
	ComposeFile   string    `json:"composeFile"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"` // "created", "deploying", "running", "stopped", "failed"
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ProjectDetail provides extended details for a project including compose content and file list.
type ProjectDetail struct {
	Project
	ComposeContent string   `json:"composeContent,omitempty"`
	Files          []string `json:"files,omitempty"`
	HasDataDir     bool     `json:"hasDataDir"`
}

// ComposeServiceStatus represents status of a single service in a compose stack.
type ComposeServiceStatus struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Service    string `json:"service"`
	State      string `json:"state"`
	Status     string `json:"status"`
	Health     string `json:"health,omitempty"`
	Publishers string `json:"publishers,omitempty"`
}

// ProjectStatusResponse holds status check results for a project stack.
type ProjectStatusResponse struct {
	Project  string                 `json:"project"`
	Status   string                 `json:"status"`
	Services []ComposeServiceStatus `json:"services"`
	Output   string                 `json:"output,omitempty"`
}

// TeardownOptions specifies options for stopping and cleaning up a compose project.
type TeardownOptions struct {
	RemoveVolumes bool `json:"removeVolumes"` // Remove named volumes via docker compose down -v (default: false)
	DeleteFiles   bool `json:"deleteFiles"`   // Remove project files on host disk (default: false)
	CreateArchive bool `json:"createArchive"` // Create a timestamped archive before deletion (default: true)
}
