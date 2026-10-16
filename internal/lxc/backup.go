package lxc

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
)

const archiveName = "container.tar.gz"

// tarFlags keep ownership (shifted IDs of unprivileged containers), file
// capabilities, ACLs and other extended attributes.
var tarFlags = []string{"--numeric-owner", "--xattrs", "--xattrs-include=*", "--acls", "--sparse"}

// countWriter counts bytes passing through.
type countWriter struct {
	w io.Writer
	n *atomic.Int64
}

func (c countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

type countReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func treeSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// ticker reports progress once per second until stop is closed.
func ticker(stop <-chan struct{}, f func()) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f()
		}
	}
}

// Backup archives a container (config and root file system). Modes:
//
//	stop    – a running container is shut down for the backup and started again
//	suspend – the container is frozen for the duration of the backup
//	live    – copied while running; files may be inconsistent
func (m *Manager) Backup(ctx context.Context, name, store, mode, notes, software string, rep backup.Reporter) (*backup.Manifest, error) {
	if err := m.check(name); err != nil {
		return nil, err
	}
	if mode == "" {
		mode = "stop"
	}
	if mode != "stop" && mode != "suspend" && mode != "live" {
		return nil, &agentapi.FieldError{Field: "mode", Reason: "stop, suspend or live"}
	}
	cur, err := m.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	running := cur.State == "running"
	if cur.State == "frozen" {
		return nil, fmt.Errorf("%w: container is frozen", ErrConflict)
	}
	start := time.Now()
	id, partial, err := backup.Begin(store, "ct", name, start)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			backup.Abort(partial)
		}
	}()
	man := &backup.Manifest{ID: id, Kind: "ct", Name: name, Created: start.UTC(), Notes: notes, Software: software, Archive: archiveName}
	man.Host, _ = os.Hostname()
	man.Mode, man.Consistency = mode, "offline"

	if running {
		switch mode {
		case "stop":
			rep.Say("Shutting down container for a consistent backup")
			if err := m.Action(ctx, name, "shutdown"); err != nil {
				return nil, fmt.Errorf("shutdown: %w", err)
			}
			defer func() {
				rep.Say("Starting container again")
				if err := m.Action(context.Background(), name, "start"); err != nil {
					rep.Say("Warning: restart failed: " + err.Error())
				}
			}()
		case "suspend":
			rep.Say("Freezing container")
			if err := m.Action(ctx, name, "freeze"); err != nil {
				return nil, fmt.Errorf("freeze: %w", err)
			}
			man.Consistency = "crash"
			defer func() {
				if err := m.Action(context.Background(), name, "unfreeze"); err != nil {
					rep.Say("Warning: unfreeze failed: " + err.Error())
				} else {
					rep.Say("Container resumed")
				}
			}()
		case "live":
			rep.Say("Copying the running container; files being written may be inconsistent")
			man.Consistency = "none"
		}
	} else {
		man.Mode = "stop"
	}

	total := treeSize(m.dir(name))
	rep.Say(fmt.Sprintf("Archiving %s (%d MiB)", m.dir(name), total>>20))
	out, err := os.OpenFile(filepath.Join(partial, archiveName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	gz, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
	var read atomic.Int64
	args := append([]string{"-c", "-p", "-f", "-", "-C", m.Path}, tarFlags...)
	args = append(args, name)
	cmd := exec.CommandContext(ctx, "tar", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Stdout = countWriter{gz, &read}
	stop := make(chan struct{})
	go ticker(stop, func() {
		if n := read.Load(); n < total {
			rep.Report(n, total)
		}
	})
	err = cmd.Run()
	close(stop)
	if err != nil {
		var ee *exec.ExitError
		// GNU tar exits 1 when files changed while being read; expected live.
		if !(mode == "live" && running && errors.As(err, &ee) && ee.ExitCode() == 1) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("tar: %v: %s", err, strings.TrimSpace(errb.String()))
		}
		rep.Say("Some files changed while being archived")
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := out.Sync(); err != nil {
		return nil, err
	}
	rep.Report(total, total)
	man.Duration = time.Since(start).Seconds()
	if err := backup.Commit(store, partial, man); err != nil {
		return nil, err
	}
	ok = true
	rep.Say(fmt.Sprintf("Backup %s complete (%s, %s)", id, man.Mode, man.Consistency))
	return man, nil
}

// Restore creates a container from a backup. The restored container keeps
// its configuration; name-dependent lines (rootfs path, hostname) are
// rewritten and fixed MAC addresses are dropped unless the original name is
// restored and no container of that name exists.
func (m *Manager) Restore(ctx context.Context, store, id string, r agentapi.Restore, rep backup.Reporter) (*agentapi.Container, error) {
	man, dir, err := backup.Read(store, id)
	if err != nil {
		return nil, err
	}
	if man.Kind != "ct" || man.Archive != archiveName {
		return nil, fmt.Errorf("%w: not a container backup", ErrConflict)
	}
	if r.Name == "" {
		r.Name = man.Name
	}
	if !agentapi.ValidContainerName(r.Name) {
		return nil, &agentapi.FieldError{Field: "name", Reason: "1-63 characters: lowercase letters, digits, '-'"}
	}
	if _, err := os.Lstat(m.dir(r.Name)); err == nil {
		return nil, &agentapi.FieldError{Field: "name", Reason: "a container with this name already exists"}
	}
	tmp, err := os.MkdirTemp(m.Path, ".hm-restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	f, err := os.Open(filepath.Join(dir, archiveName))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, _ := f.Stat()
	var read atomic.Int64
	args := append([]string{"-x", "-p", "-z", "-f", "-", "-C", tmp}, tarFlags...)
	cmd := exec.CommandContext(ctx, "tar", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	var errb strings.Builder
	cmd.Stderr = &errb
	cmd.Stdin = countReader{f, &read}
	rep.Say("Extracting archive")
	stop := make(chan struct{})
	go ticker(stop, func() { rep.Report(read.Load(), fi.Size()) })
	err = cmd.Run()
	close(stop)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("tar: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	rep.Report(fi.Size(), fi.Size())
	src := filepath.Join(tmp, man.Name)
	if _, err := os.Stat(filepath.Join(src, "config")); err != nil {
		return nil, fmt.Errorf("archive does not contain %s/config", man.Name)
	}
	keepIdentity := r.Name == man.Name
	if err := rewriteConfig(filepath.Join(src, "config"), man.Name, r.Name, m.Path, keepIdentity); err != nil {
		return nil, err
	}
	if err := os.Rename(src, m.dir(r.Name)); err != nil {
		return nil, err
	}
	if r.Name != man.Name {
		rep.Say("Restored as " + r.Name + "; the hostname inside the guest is unchanged")
	}
	if r.Start {
		rep.Say("Starting " + r.Name)
		if err := m.Action(ctx, r.Name, "start"); err != nil {
			return nil, fmt.Errorf("container restored, but start failed: %w", err)
		}
	}
	return m.Get(ctx, r.Name)
}

func rewriteConfig(path, oldName, newName, base string, keepIdentity bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		k, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		k = strings.TrimSpace(k)
		switch {
		case k == "lxc.rootfs.path":
			line = "lxc.rootfs.path = dir:" + filepath.Join(base, newName, "rootfs")
		case k == "lxc.uts.name" && newName != oldName:
			line = "lxc.uts.name = " + newName
		case strings.HasPrefix(k, "lxc.net.") && strings.HasSuffix(k, ".hwaddr") && !keepIdentity:
			continue
		case k == "lxc.mount.fstab":
			// Points into the old container directory.
			line = strings.Replace(line, filepath.Join(base, oldName)+"/", filepath.Join(base, newName)+"/", 1)
		}
		out = append(out, line)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o640)
}
