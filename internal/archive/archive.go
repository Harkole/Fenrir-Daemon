package archive

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/compose"
	"github.com/Harkole/fenrir-daemon/internal/database"
	"github.com/Harkole/fenrir-daemon/internal/model"
)

// CreateZipArchive bundles a project directory (configs and persistent data/ directory) into a timestamped .zip archive.
func CreateZipArchive(ctx context.Context, projectName, projectDir, archivesDir string, retentionDays int) (*model.ArchiveMetadata, error) {
	if strings.TrimSpace(projectName) == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	if _, err := os.Stat(projectDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("project directory %q does not exist", projectDir)
	}

	if retentionDays <= 0 {
		retentionDays = 90
	}

	if err := os.MkdirAll(archivesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create archives directory %q: %w", archivesDir, err)
	}

	now := time.Now().UTC()
	timestampStr := now.Format("20060102-150405")
	archiveFilename := fmt.Sprintf("%s-%s.zip", projectName, timestampStr)
	archivePath := filepath.Join(archivesDir, archiveFilename)

	zipFile, err := os.Create(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create archive zip file %q: %w", archivePath, err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Walk project directory and compress all files
	err = filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relPath, err := filepath.Rel(projectDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		// Normalize to forward slashes for zip file portability
		zipEntryName := filepath.ToSlash(relPath)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("failed to create zip header for %q: %w", relPath, err)
		}
		header.Name = zipEntryName

		if info.IsDir() {
			header.Name += "/"
			_, err = zipWriter.CreateHeader(header)
			return err
		}

		header.Method = zip.Deflate
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("failed to add zip entry %q: %w", zipEntryName, err)
		}

		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open source file %q: %w", path, err)
		}
		defer file.Close()

		if _, err := io.Copy(writer, file); err != nil {
			return fmt.Errorf("failed to write zip file data for %q: %w", zipEntryName, err)
		}

		return nil
	})

	if err != nil {
		_ = zipFile.Close()
		_ = os.Remove(archivePath)
		return nil, fmt.Errorf("failed to bundle project archive: %w", err)
	}

	if err := zipWriter.Close(); err != nil {
		_ = zipFile.Close()
		_ = os.Remove(archivePath)
		return nil, fmt.Errorf("failed to finalize zip archive: %w", err)
	}

	stat, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat archive %q: %w", archivePath, err)
	}

	expiresAt := now.Add(time.Duration(retentionDays) * 24 * time.Hour)

	return &model.ArchiveMetadata{
		ProjectName:       projectName,
		ArchivePath:       archivePath,
		SizeBytes:         stat.Size(),
		CreatedAt:         now,
		ExpiresAt:         expiresAt,
		DaysUntilDeletion: model.CalculateDaysUntilDeletion(expiresAt),
	}, nil
}

// RestoreZipArchive extracts an archive back into the target project directory and enforces container permissions.
func RestoreZipArchive(ctx context.Context, archivePath, targetProjectDir string, uid, gid int) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open zip archive %q: %w", archivePath, err)
	}
	defer reader.Close()

	if err := os.MkdirAll(targetProjectDir, 0755); err != nil {
		return fmt.Errorf("failed to create target project directory %q: %w", targetProjectDir, err)
	}

	cleanTarget := filepath.Clean(targetProjectDir)

	for _, file := range reader.File {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		targetPath := filepath.Join(cleanTarget, file.Name)

		// Guard against Zip Slip vulnerability
		cleanDestination := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanDestination, cleanTarget+string(os.PathSeparator)) && cleanDestination != cleanTarget {
			return fmt.Errorf("illegal file path in zip archive: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanDestination, file.Mode()); err != nil {
				return fmt.Errorf("failed to create restored directory %q: %w", cleanDestination, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanDestination), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %q: %w", cleanDestination, err)
		}

		outFile, err := os.OpenFile(cleanDestination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return fmt.Errorf("failed to create restored file %q: %w", cleanDestination, err)
		}

		rc, err := file.Open()
		if err != nil {
			_ = outFile.Close()
			return fmt.Errorf("failed to read zip entry %q: %w", file.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		_ = rc.Close()
		_ = outFile.Close()
		if err != nil {
			return fmt.Errorf("failed to copy restored file %q: %w", cleanDestination, err)
		}
	}

	// Recursively set file ownership/permissions for container users (e.g. UID/GID 1000)
	if err := compose.SetStackPermissions(cleanTarget, uid, gid); err != nil {
		log.Printf("warn: failed to set permissions after archive restore on %s: %v", cleanTarget, err)
	}

	return nil
}

// PruneExpiredArchives checks the database for expired archives older than 90 days, removes the zip files from disk, and cleans up the SQLite records.
func PruneExpiredArchives(ctx context.Context, db *database.DB) (int64, error) {
	if db == nil {
		return 0, nil
	}

	expiredList, err := db.GetExpiredArchives(ctx, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to query expired archives: %w", err)
	}

	var prunedCount int64
	for _, a := range expiredList {
		// Remove physical zip file from disk
		if err := os.Remove(a.ArchivePath); err != nil && !os.IsNotExist(err) {
			log.Printf("[WARN] Failed to delete expired archive file %q: %v", a.ArchivePath, err)
		}

		// Delete record from database
		if err := db.DeleteArchive(ctx, a.ID); err != nil {
			log.Printf("[WARN] Failed to delete expired archive record %d: %v", a.ID, err)
		} else {
			prunedCount++
			log.Printf("[INFO] Pruned expired archive %q (project: %s, id: %d)", filepath.Base(a.ArchivePath), a.ProjectName, a.ID)
		}
	}

	return prunedCount, nil
}
