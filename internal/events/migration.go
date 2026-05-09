package events

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MigrateLegacyLedger relocates pre-E-1197 ledger files to their new home.
//
// Pre-E-1197: .endless/events/events-{nodeHex}-{seq:06d}.jsonl
// Post-E-1197: .endless/db-ledger/db-entries-{nodeHex}-{seq:06d}.jsonl
//
// The migration is idempotent. It is safe to call on every NewWriter init.
// Behavior:
//
//  1. If .endless/events/ does not exist: no-op.
//  2. If .endless/events/ exists but is empty (or only contains directories):
//     remove the empty dir, no-op.
//  3. If .endless/db-ledger/ already exists with any db-entries-*.jsonl files,
//     refuse migration (mixed state — caller must reconcile manually). Returns
//     a descriptive error.
//  4. Otherwise: for each events-*.jsonl in the legacy dir, copy its contents
//     to db-entries-*.jsonl in the new dir, line-count-verify, then remove
//     the source. After all files migrate successfully, remove the empty
//     legacy dir.
//
// We copy-then-delete (not rename) to give us a recovery path if something
// fails mid-way: the source remains intact until the destination is verified.
//
// This function is the load-bearing safety mechanism for the rename. The
// 2026-05-06 incident (E-1170 land) showed how easy it is to silently lose
// these files, so the verify-before-delete pattern is non-negotiable.
func MigrateLegacyLedger(projectRoot string) error {
	legacyDir := filepath.Join(projectRoot, ".endless", LegacyDirName)
	newDir := filepath.Join(projectRoot, ".endless", LedgerDirName)

	legacyInfo, err := os.Stat(legacyDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy dir: %w", err)
	}
	if !legacyInfo.IsDir() {
		return fmt.Errorf("expected directory at %s, found non-directory", legacyDir)
	}

	legacyFiles, err := listLegacyEntries(legacyDir)
	if err != nil {
		return err
	}

	if len(legacyFiles) == 0 {
		// Empty legacy dir — clean up and bail.
		_ = os.Remove(legacyDir)
		return nil
	}

	// Refuse mixed-state: if new dir already has db-entries-*.jsonl files,
	// merging requires human judgment about ordering and dedup.
	if existing, err := listNewEntries(newDir); err != nil {
		return err
	} else if len(existing) > 0 {
		return fmt.Errorf(
			"both %s and %s contain ledger files; refusing automatic migration. "+
				"Resolve manually: pick the canonical source, move files into %s with the new prefix, "+
				"and remove the other directory.",
			legacyDir, newDir, newDir)
	}

	if err := os.MkdirAll(newDir, 0755); err != nil {
		return fmt.Errorf("create new dir: %w", err)
	}

	for _, name := range legacyFiles {
		newName := LedgerFilePrefix + strings.TrimPrefix(name, LegacyFilePrefix)
		src := filepath.Join(legacyDir, name)
		dst := filepath.Join(newDir, newName)
		if err := migrateFile(src, dst); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}

	// All files migrated and verified. Remove the now-empty legacy dir.
	if err := os.Remove(legacyDir); err != nil && !os.IsNotExist(err) {
		// Non-fatal: migration succeeded, just couldn't clean up.
		return fmt.Errorf("migration complete, but failed to remove legacy dir %s: %w", legacyDir, err)
	}
	return nil
}

func listLegacyEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read legacy dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, LegacyFilePrefix) && strings.HasSuffix(name, LedgerFileSuffix) {
			files = append(files, name)
		}
	}
	return files, nil
}

func listNewEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read new dir: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, LedgerFilePrefix) && strings.HasSuffix(name, LedgerFileSuffix) {
			files = append(files, name)
		}
	}
	return files, nil
}

// migrateFile copies src to dst, then verifies dst has the same line count
// as src before removing src. If dst already exists, it is overwritten only
// when its content equals src's content (idempotent re-run of a previous
// migration that failed mid-cleanup).
func migrateFile(src, dst string) error {
	srcBytes, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}

	if existing, err := os.ReadFile(dst); err == nil {
		if bytes.Equal(existing, srcBytes) {
			// Prior migration left dst behind; safe to remove src and continue.
			return os.Remove(src)
		}
		return fmt.Errorf("destination %s already exists with different content; refusing to overwrite", dst)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat destination: %w", err)
	}

	tmp := dst + ".tmp"
	if err := writeFileSync(tmp, srcBytes); err != nil {
		return fmt.Errorf("write destination: %w", err)
	}

	srcLines := bytes.Count(srcBytes, []byte{'\n'})
	dstBytes, err := os.ReadFile(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("verify read: %w", err)
	}
	dstLines := bytes.Count(dstBytes, []byte{'\n'})
	if srcLines != dstLines {
		_ = os.Remove(tmp)
		return fmt.Errorf("line count mismatch (src=%d, dst=%d) — refusing to remove source", srcLines, dstLines)
	}
	if !bytes.Equal(srcBytes, dstBytes) {
		_ = os.Remove(tmp)
		return fmt.Errorf("content mismatch after copy — refusing to remove source")
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit destination: %w", err)
	}

	return os.Remove(src)
}

// writeFileSync writes data to path, fsyncing before close.
func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		return err
	}
	return f.Sync()
}
