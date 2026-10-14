package compose

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
)

// SetStackPermissions recursively sets permissions and ownership on rootPath to ensure
// files and directories are accessible to container users (such as UID/GID 1000 for Minecraft/Java).
//
// On Unix systems, it chowns all files/directories to the given uid:gid (if running with sufficient privileges)
// and applies 0775 to directories and 0664 (or 0775 for executables) to files.
// On Windows, ownership is bypassed and directory/file permissions are applied safely.
func SetStackPermissions(rootPath string, uid, gid int) error {
	_, err := os.Stat(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	return filepath.Walk(rootPath, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Apply directory or file permissions
		if fi.IsDir() {
			if err := os.Chmod(path, 0775); err != nil {
				// Don't fail if filesystem doesn't support chmod (e.g. some virtual mounts)
				log.Printf("warn: could not chmod directory %s: %v", path, err)
			}
		} else {
			// If file has any execute bits set, maintain execute permission (0775), else 0664
			mode := os.FileMode(0664)
			if fi.Mode()&0111 != 0 {
				mode = 0775
			}
			if err := os.Chmod(path, mode); err != nil {
				log.Printf("warn: could not chmod file %s: %v", path, err)
			}
		}

		// Apply ownership on Unix-like operating systems
		if runtime.GOOS != "windows" && uid >= 0 && gid >= 0 {
			if err := os.Chown(path, uid, gid); err != nil {
				// If not running as root, os.Chown will return EPERM. Log warning rather than hard-failing
				if os.IsPermission(err) {
					log.Printf("warn: insufficient privileges to chown %s to %d:%d: %v", path, uid, gid, err)
				} else {
					return err
				}
			}
		}

		return nil
	})
}
