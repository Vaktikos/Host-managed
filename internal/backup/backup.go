// Package backup manages the on-disk layout of Host-managed backups:
//
//	<store>/<kind>/<name>/<timestamp>/manifest.json
//
// kind is "vm" or "ct". A backup is written to "<timestamp>.partial" and
// renamed when complete, so listings never show half-written backups.
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const TimeFormat = "2006-01-02T15-04-05Z"

// FormatVersion of the manifest; readers refuse newer versions.
const FormatVersion = 1

var (
	idRe   = regexp.MustCompile(`^(vm|ct)/([a-zA-Z0-9][a-zA-Z0-9._-]{0,62})/(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}Z)$`)
	ErrBad = errors.New("invalid backup id")
)

type Disk struct {
	Target   string `json:"target"`
	Bus      string `json:"bus"`
	File     string `json:"file"`
	Format   string `json:"format"`
	Capacity uint64 `json:"capacity"`
}

type Manifest struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Host     string    `json:"host"`
	Software string    `json:"software"`
	// Mode: snapshot (running VM, backup job), offline (VM off), stop
	// (container stopped during backup), live (container running).
	Mode string `json:"mode"`
	// Consistency: application (guest file systems frozen), crash (like a
	// power loss), offline (VM/container was not running), none (live
	// container copy without freeze – may be inconsistent).
	Consistency string  `json:"consistency"`
	Disks       []Disk  `json:"disks,omitempty"`
	NVRAM       string  `json:"nvram,omitempty"`
	Archive     string  `json:"archive,omitempty"` // container tarball
	Size        int64   `json:"size"`
	Notes       string  `json:"notes,omitempty"`
	Protected   bool    `json:"protected"`
	Duration    float64 `json:"duration_seconds"`
}

// Reporter receives progress of long operations.
type Reporter struct {
	Log      func(string)
	Progress func(done, total int64)
}

func (r Reporter) Say(s string) {
	if r.Log != nil {
		r.Log(s)
	}
}

func (r Reporter) Report(done, total int64) {
	if r.Progress != nil {
		r.Progress(done, total)
	}
}

// ParseID validates "kind/name/timestamp".
func ParseID(id string) (kind, name string, ts time.Time, err error) {
	m := idRe.FindStringSubmatch(id)
	if m == nil {
		return "", "", time.Time{}, ErrBad
	}
	ts, err = time.Parse(TimeFormat, m[3])
	if err != nil {
		return "", "", time.Time{}, ErrBad
	}
	return m[1], m[2], ts, nil
}

func NewID(kind, name string, t time.Time) string {
	return kind + "/" + name + "/" + t.UTC().Format(TimeFormat)
}

// Dir returns the backup directory for id inside store.
func Dir(store, id string) (string, error) {
	if _, _, _, err := ParseID(id); err != nil {
		return "", err
	}
	d := filepath.Join(store, filepath.FromSlash(id))
	if !strings.HasPrefix(d, filepath.Clean(store)+string(os.PathSeparator)) {
		return "", ErrBad
	}
	return d, nil
}

// Begin creates the partial directory for a new backup.
func Begin(store, kind, name string, now time.Time) (id, partial string, err error) {
	id = NewID(kind, name, now)
	d, err := Dir(store, id)
	if err != nil {
		return "", "", err
	}
	// Directories are traversable because QEMU (running unprivileged) writes
	// push-backup targets itself; the files inside are 0600.
	if err := os.MkdirAll(filepath.Dir(d), 0o755); err != nil {
		return "", "", err
	}
	partial = d + ".partial"
	if err := os.Mkdir(partial, 0o755); err != nil {
		return "", "", fmt.Errorf("start backup: %w", err)
	}
	return id, partial, nil
}

// Commit writes the manifest and makes the backup visible.
func Commit(store, partial string, m *Manifest) error {
	m.Version = FormatVersion
	var size int64
	_ = filepath.Walk(partial, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			size += fi.Size()
		}
		return nil
	})
	m.Size = size
	if err := writeManifest(filepath.Join(partial, "manifest.json"), m); err != nil {
		return err
	}
	final, err := Dir(store, m.ID)
	if err != nil {
		return err
	}
	return os.Rename(partial, final)
}

// Abort removes a partial backup.
func Abort(partial string) {
	if strings.HasSuffix(partial, ".partial") {
		_ = os.RemoveAll(partial)
	}
}

func writeManifest(path string, m *Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func Read(store, id string) (*Manifest, string, error) {
	d, err := Dir(store, id)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(filepath.Join(d, "manifest.json"))
	if err != nil {
		return nil, "", err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, "", fmt.Errorf("corrupt manifest: %w", err)
	}
	if m.Version > FormatVersion {
		return nil, "", fmt.Errorf("backup format %d is newer than supported (%d)", m.Version, FormatVersion)
	}
	if m.ID != id {
		return nil, "", fmt.Errorf("manifest id %q does not match %q", m.ID, id)
	}
	return &m, d, nil
}

// List returns all complete backups in store, newest first.
func List(store string) ([]Manifest, error) {
	var out []Manifest
	for _, kind := range []string{"vm", "ct"} {
		names, err := os.ReadDir(filepath.Join(store, kind))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, n := range names {
			if !n.IsDir() {
				continue
			}
			ents, _ := os.ReadDir(filepath.Join(store, kind, n.Name()))
			for _, e := range ents {
				if !e.IsDir() || strings.HasSuffix(e.Name(), ".partial") {
					continue
				}
				id := kind + "/" + n.Name() + "/" + e.Name()
				if m, _, err := Read(store, id); err == nil {
					out = append(out, *m)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func SetProtected(store, id string, protected bool) error {
	m, d, err := Read(store, id)
	if err != nil {
		return err
	}
	m.Protected = protected
	return writeManifest(filepath.Join(d, "manifest.json"), m)
}

// ErrProtected is returned when deleting a protected backup.
var ErrProtected = errors.New("backup is protected")

func Delete(store, id string) error {
	m, d, err := Read(store, id)
	if err != nil {
		return err
	}
	if m.Protected {
		return ErrProtected
	}
	return os.RemoveAll(d)
}

// CleanPartial removes partial backups left by an interrupted run. Only
// call it when no backup can be running (agent start-up).
func CleanPartial(store string) []string {
	var removed []string
	for _, kind := range []string{"vm", "ct"} {
		names, _ := os.ReadDir(filepath.Join(store, kind))
		for _, n := range names {
			ents, _ := os.ReadDir(filepath.Join(store, kind, n.Name()))
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".partial") {
					p := filepath.Join(store, kind, n.Name(), e.Name())
					_ = os.RemoveAll(p)
					removed = append(removed, p)
				}
			}
		}
	}
	return removed
}
