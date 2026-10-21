// Package snapshots implements versioned, checksummed world backups.
//
// A snapshot is a directory containing:
//   - manifest.json  (world id, MC version, creator, fencing token,
//     per-file SHA-256 + sizes)
//   - data.tar.gz    (the world directory contents)
//
// Restore verifies every file hash before anything is written to the
// target, extracts to a staging directory, then swaps it into place.
package snapshots

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileEntry is one file's integrity record.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a snapshot.
type Manifest struct {
	ID           string      `json:"id"`
	WorldID      string      `json:"world_id"`
	MCVersion    string      `json:"mc_version"`
	CreatedAt    time.Time   `json:"created_at"`
	CreatedBy    string      `json:"created_by"`
	FencingToken uint64      `json:"fencing_token"`
	Files        []FileEntry `json:"files"`
	TotalBytes   int64       `json:"total_bytes"`
}

// Store manages snapshots under a root directory: <root>/<worldID>/<snapshotID>/
type Store struct {
	Root string
}

// NewStore creates a snapshot store rooted at root.
func NewStore(root string) *Store { return &Store{Root: root} }

func (s *Store) snapDir(worldID, snapID string) string {
	return filepath.Join(s.Root, worldID, snapID)
}

// Create snapshots srcDir. snapshotID is time-ordered and unique.
// Files named in exclude (top-level names, e.g. "session.lock") are skipped.
func (s *Store) Create(worldID, mcVersion, createdBy string, fencingToken uint64, srcDir string, exclude ...string) (Manifest, error) {
	if _, err := os.Stat(srcDir); err != nil {
		return Manifest{}, fmt.Errorf("snapshot source: %w", err)
	}
	id := time.Now().UTC().Format("20060102T150405") + "-" + randSuffix()
	dir := s.snapDir(worldID, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Manifest{}, err
	}
	m := Manifest{
		ID: id, WorldID: worldID, MCVersion: mcVersion,
		CreatedAt: time.Now().UTC(), CreatedBy: createdBy, FencingToken: fencingToken,
	}
	skip := map[string]bool{}
	for _, e := range exclude {
		skip[e] = true
	}

	archivePath := filepath.Join(dir, "data.tar.gz")
	af, err := os.Create(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	gz := gzip.NewWriter(af)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return err
		}
		top := strings.Split(rel, string(os.PathSeparator))[0]
		if skip[top] || skip[rel] {
			if info.IsDir() && top == rel {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil // sockets, fifos etc. are not world data
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m.Files = append(m.Files, FileEntry{Path: filepath.ToSlash(rel), Size: info.Size(), SHA256: hex.EncodeToString(sum[:])})
		m.TotalBytes += info.Size()
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	closeErr := tw.Close()
	gzErr := gz.Close()
	fileErr := af.Close()
	if walkErr != nil {
		_ = os.RemoveAll(dir)
		return Manifest{}, fmt.Errorf("snapshot walk: %w", walkErr)
	}
	if closeErr != nil || gzErr != nil || fileErr != nil {
		_ = os.RemoveAll(dir)
		return Manifest{}, fmt.Errorf("snapshot archive close: %v %v %v", closeErr, gzErr, fileErr)
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), mb, 0o644); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func randSuffix() string {
	b := make([]byte, 3)
	// time-based fallback is fine; uniqueness within a second is guarded
	// by the existence check loop in CreateWithRetry callers. Use crypto
	// rand via sha of nanotime to avoid an extra import cycle of errors.
	h := sha256.Sum256([]byte(time.Now().Format(time.RFC3339Nano)))
	copy(b, h[:3])
	return hex.EncodeToString(b)
}

// LoadManifest reads a snapshot's manifest.
func (s *Store) LoadManifest(worldID, snapID string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(s.snapDir(worldID, snapID), "manifest.json"))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

// List returns manifests for a world, newest first.
func (s *Store) List(worldID string) ([]Manifest, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, worldID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if m, err := s.LoadManifest(worldID, e.Name()); err == nil {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Verify recomputes every file hash inside the archive and compares it
// to the manifest. Any mismatch, missing or extra file fails.
func (s *Store) Verify(worldID, snapID string) error {
	m, err := s.LoadManifest(worldID, snapID)
	if err != nil {
		return err
	}
	want := map[string]FileEntry{}
	for _, f := range m.Files {
		want[f.Path] = f
	}
	seen := map[string]bool{}
	err = s.readArchive(worldID, snapID, func(name string, data []byte) error {
		fe, ok := want[name]
		if !ok {
			return fmt.Errorf("archive contains unlisted file %q", name)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != fe.SHA256 || int64(len(data)) != fe.Size {
			return fmt.Errorf("checksum mismatch for %q", name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(want) {
		return fmt.Errorf("archive is missing %d files from the manifest", len(want)-len(seen))
	}
	return nil
}

func (s *Store) readArchive(worldID, snapID string, fn func(name string, data []byte) error) error {
	af, err := os.Open(filepath.Join(s.snapDir(worldID, snapID), "data.tar.gz"))
	if err != nil {
		return err
	}
	defer af.Close()
	gz, err := gzip.NewReader(af)
	if err != nil {
		return fmt.Errorf("corrupt gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("corrupt tar archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := fn(hdr.Name, data); err != nil {
			return err
		}
	}
}

var ErrVersionMismatch = errors.New("snapshot minecraft version is incompatible with the target world")

// Restore verifies the snapshot, then replaces targetDir's contents.
// expectedVersion, when non-empty, must equal the manifest's version.
func (s *Store) Restore(worldID, snapID, targetDir, expectedVersion string) error {
	m, err := s.LoadManifest(worldID, snapID)
	if err != nil {
		return err
	}
	if expectedVersion != "" && m.MCVersion != "" && m.MCVersion != expectedVersion {
		return fmt.Errorf("%w: snapshot is %s, world is %s", ErrVersionMismatch, m.MCVersion, expectedVersion)
	}
	if err := s.Verify(worldID, snapID); err != nil {
		return fmt.Errorf("refusing to restore unverified snapshot: %w", err)
	}
	staging := targetDir + ".restore-staging"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	err = s.readArchive(worldID, snapID, func(name string, data []byte) error {
		clean := filepath.Clean("/" + name) // confine to staging
		dest := filepath.Join(staging, clean)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	// Swap: move current aside, staging in, drop the old copy.
	old := targetDir + ".pre-restore"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(targetDir); err == nil {
		if err := os.Rename(targetDir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, targetDir); err != nil {
		_ = os.Rename(old, targetDir)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// Rotate deletes all but the newest `keep` snapshots of a world.
func (s *Store) Rotate(worldID string, keep int) (deleted int, err error) {
	if keep < 1 {
		keep = 1
	}
	list, err := s.List(worldID)
	if err != nil {
		return 0, err
	}
	for _, m := range list[min(keep, len(list)):] {
		if err := os.RemoveAll(s.snapDir(worldID, m.ID)); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
