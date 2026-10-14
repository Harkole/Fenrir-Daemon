package model

import (
	"math"
	"time"
)

// ArchiveMetadata represents metadata for a stored stack backup archive.
type ArchiveMetadata struct {
	ID                int64     `json:"id"`
	ProjectName       string    `json:"projectName"`
	ArchivePath       string    `json:"archivePath"`
	SizeBytes         int64     `json:"sizeBytes"`
	CreatedAt         time.Time `json:"createdAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
	DaysUntilDeletion int       `json:"daysUntilDeletion"`
}

// CalculateDaysUntilDeletion computes the remaining days until the archive reaches expiration.
func CalculateDaysUntilDeletion(expiresAt time.Time) int {
	duration := time.Until(expiresAt)
	if duration <= 0 {
		return 0
	}
	days := int(math.Ceil(duration.Hours() / 24.0))
	if days < 0 {
		return 0
	}
	return days
}
