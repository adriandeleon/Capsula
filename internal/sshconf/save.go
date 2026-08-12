package sshconf

import (
	"fmt"
	"os"
	"path/filepath"
)

// BackupSuffix is appended to the previous contents of a file before it is
// overwritten.
const BackupSuffix = ".capsula.bak"

// Save writes every file in the set that has unsaved changes.
//
// Each write is atomic: the new contents go to a temporary file in the same
// directory, are flushed to disk, and are then renamed over the target. A crash
// mid-save therefore leaves either the old file or the new one, never a
// half-written config — which for ~/.ssh/config could mean locking the user out
// of every host they have.
func (s *Set) Save() error {
	for _, f := range s.Files {
		if !f.dirty {
			continue
		}
		if err := f.Save(); err != nil {
			return err
		}
	}
	return nil
}

// Save writes a single file, whether or not it is marked dirty.
func (f *File) Save() error {
	dir := filepath.Dir(f.Path)
	// ~/.ssh may not exist yet. ssh refuses to use a group- or world-readable
	// directory, so create it the way ssh-keygen would.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	mode := os.FileMode(0o600)
	if st, err := os.Stat(f.Path); err == nil {
		// Keep whatever the user chose, minus any group/world write bits, which
		// ssh would reject.
		mode = st.Mode().Perm() &^ 0o022
	}

	data := f.Bytes()

	tmp, err := os.CreateTemp(dir, ".capsula-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename has succeeded

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}

	if err := backup(f.Path, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, f.Path); err != nil {
		return fmt.Errorf("replace %s: %w", f.Path, err)
	}

	f.dirty = false
	return nil
}

// backup copies the current contents of path aside before it is replaced. A
// missing original is fine — there is nothing to preserve.
func backup(path string, mode os.FileMode) error {
	prev, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s for backup: %w", path, err)
	}
	if err := os.WriteFile(path+BackupSuffix, prev, mode); err != nil {
		return fmt.Errorf("write backup %s: %w", path+BackupSuffix, err)
	}
	return nil
}
