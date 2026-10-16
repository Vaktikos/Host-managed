package virt

import (
	"bufio"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	"libvirt.org/go/libvirtxml"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/backup"
)

// Reporter receives progress of long operations.
type Reporter = backup.Reporter

var qemuImgPct = regexp.MustCompile(`\((\d+(?:\.\d+)?)/100%\)`)

// qemuImgConvert copies a disk image, reporting qemu-img's own progress.
func qemuImgConvert(ctx context.Context, args []string, onPct func(float64)) error {
	cmd := exec.CommandContext(ctx, "qemu-img", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		for i, b := range data {
			if b == '\r' || b == '\n' {
				return i + 1, data[:i], nil
			}
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for sc.Scan() {
		if m := qemuImgPct.FindStringSubmatch(sc.Text()); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				onPct(v)
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("qemu-img: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}

type backupDisk struct {
	target, bus, path, format string
	capacity                  uint64
}

func (m *Manager) backupDisks(l *libvirt.Libvirt, x *libvirtxml.Domain) ([]backupDisk, error) {
	var out []backupDisk
	if x.Devices == nil {
		return nil, nil
	}
	for _, dk := range x.Devices.Disks {
		if dk.Device != "disk" || dk.Target == nil {
			continue
		}
		p := diskPath(dk)
		if p == "" {
			return nil, fmt.Errorf("%w: disk %s has no file or block source", ErrConflict, dk.Target.Dev)
		}
		bd := backupDisk{target: dk.Target.Dev, bus: dk.Target.Bus, path: p, format: "raw"}
		if dk.Driver != nil && dk.Driver.Type != "" {
			bd.format = dk.Driver.Type
		}
		if v, err := l.StorageVolLookupByPath(p); err == nil {
			if _, c, _, err := l.StorageVolGetInfo(v); err == nil {
				bd.capacity = c
			}
		}
		out = append(out, bd)
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// BackupVM writes a full backup of a VM into store. Running VMs are backed
// up through a libvirt push backup job, which captures a point-in-time
// state; with a responsive guest agent the file systems are frozen for the
// moment the job starts (application consistent), otherwise the backup is
// crash consistent. Shut-off VMs are copied with qemu-img.
func (m *Manager) BackupVM(ctx context.Context, name, store, notes, software string, rep Reporter) (*backup.Manifest, error) {
	l, dom, err := m.lookup(name)
	if err != nil {
		return nil, err
	}
	x, raw, err := m.domainXML(l, dom, true)
	if err != nil {
		return nil, err
	}
	disks, err := m.backupDisks(l, x)
	if err != nil {
		return nil, err
	}
	if len(disks) == 0 {
		return nil, fmt.Errorf("%w: the VM has no disks to back up", ErrConflict)
	}
	start := time.Now()
	id, partial, err := backup.Begin(store, "vm", name, start)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			backup.Abort(partial)
		}
	}()
	if err := os.WriteFile(filepath.Join(partial, "domain.xml"), []byte(raw), 0o600); err != nil {
		return nil, err
	}
	man := &backup.Manifest{ID: id, Kind: "vm", Name: name, Created: start.UTC(), Notes: notes, Software: software}
	man.Host, _ = os.Hostname()
	if x.OS != nil && x.OS.NVRam != nil && x.OS.NVRam.NVRam != "" {
		if err := copyFile(x.OS.NVRam.NVRam, filepath.Join(partial, "nvram.fd")); err == nil {
			man.NVRAM = "nvram.fd"
		} else {
			rep.Say("Warning: UEFI variables not saved: " + err.Error())
		}
	}
	var total int64
	for _, d := range disks {
		total += int64(d.capacity)
	}
	for _, d := range disks {
		man.Disks = append(man.Disks, backup.Disk{Target: d.target, Bus: d.bus, File: "disk-" + d.target + ".qcow2", Format: "qcow2", Capacity: d.capacity})
	}

	if isRunning(l, dom) {
		if err := m.pushBackup(ctx, l, dom, x, disks, partial, total, man, rep); err != nil {
			return nil, err
		}
	} else {
		man.Mode, man.Consistency = "offline", "offline"
		var before int64
		for _, d := range disks {
			rep.Say(fmt.Sprintf("Copying %s (%s, %d GiB)", d.target, d.path, d.capacity>>30))
			dst := filepath.Join(partial, "disk-"+d.target+".qcow2")
			err := qemuImgConvert(ctx, []string{"convert", "-p", "-f", d.format, "-O", "qcow2", d.path, dst}, func(p float64) {
				rep.Report(before+int64(p/100*float64(d.capacity)), total)
			})
			if err != nil {
				return nil, err
			}
			before += int64(d.capacity)
			// The VM must not have been started meanwhile (the agent's VM lock
			// prevents it; this is a second line of defence).
			if isRunning(l, dom) {
				return nil, fmt.Errorf("%w: the VM was started during an offline backup", ErrConflict)
			}
		}
	}
	man.Duration = time.Since(start).Seconds()
	if err := backup.Commit(store, partial, man); err != nil {
		return nil, err
	}
	ok = true
	rep.Say(fmt.Sprintf("Backup %s complete (%s, %s consistent)", id, man.Mode, man.Consistency))
	return man, nil
}

func (m *Manager) pushBackup(ctx context.Context, l *libvirt.Libvirt, dom libvirt.Domain, x *libvirtxml.Domain, disks []backupDisk, dir string, total int64, man *backup.Manifest, rep Reporter) error {
	man.Mode, man.Consistency = "snapshot", "crash"
	include := map[string]string{}
	for _, d := range disks {
		include[d.target] = filepath.Join(dir, "disk-"+d.target+".qcow2")
	}
	var b strings.Builder
	b.WriteString("<domainbackup mode='push'><disks>")
	for _, dk := range x.Devices.Disks {
		if dk.Target == nil {
			continue
		}
		if p, ok := include[dk.Target.Dev]; ok {
			b.WriteString("<disk name='")
			_ = xml.EscapeText(&b, []byte(dk.Target.Dev))
			b.WriteString("' backup='yes' type='file'><driver type='qcow2'/><target file='")
			_ = xml.EscapeText(&b, []byte(p))
			b.WriteString("'/></disk>")
		} else {
			b.WriteString("<disk name='")
			_ = xml.EscapeText(&b, []byte(dk.Target.Dev))
			b.WriteString("' backup='no'/>")
		}
	}
	b.WriteString("</disks></domainbackup>")

	frozen := false
	if hasAgentChannel(x) {
		if n, err := l.DomainFsfreeze(dom, nil, 0); err == nil {
			frozen = true
			rep.Say(fmt.Sprintf("Guest file systems frozen (%d)", n))
		} else {
			rep.Say("Guest agent did not freeze the file systems; the backup will be crash consistent")
		}
	} else {
		rep.Say("No guest agent channel; the backup will be crash consistent")
	}
	err := l.DomainBackupBegin(dom, b.String(), nil, 0)
	if frozen {
		if _, terr := l.DomainFsthaw(dom, nil, 0); terr != nil {
			rep.Say("Warning: thawing guest file systems failed: " + terr.Error())
		} else {
			rep.Say("Guest file systems thawed")
		}
		if err == nil {
			man.Consistency = "application"
		}
	}
	if err != nil {
		return fmt.Errorf("start backup job: %w", wrap(err))
	}
	rep.Say("Backup job running")
	for {
		select {
		case <-ctx.Done():
			_ = l.DomainAbortJob(dom)
			return ctx.Err()
		case <-time.After(time.Second):
		}
		typ, _, _, dataTotal, dataProcessed, _, _, _, _, _, _, _, err := l.DomainGetJobInfo(dom)
		if err != nil {
			return wrap(err)
		}
		if libvirt.DomainJobType(typ) == libvirt.DomainJobUnbounded || libvirt.DomainJobType(typ) == libvirt.DomainJobBounded {
			if dataTotal > 0 {
				rep.Report(int64(dataProcessed), int64(dataTotal))
			}
			continue
		}
		st, params, err := l.DomainGetJobStats(dom, libvirt.DomainJobStatsCompleted)
		if err != nil {
			return wrap(err)
		}
		if libvirt.DomainJobType(st) == libvirt.DomainJobCompleted {
			rep.Report(total, total)
			return nil
		}
		msg := "backup job failed"
		for _, p := range params {
			if p.Field == "errmsg" {
				if s, ok := p.Value.I.(string); ok {
					msg += ": " + s
				}
			}
		}
		return fmt.Errorf("%s", msg)
	}
}

func hasAgentChannel(x *libvirtxml.Domain) bool {
	if x.Devices == nil {
		return false
	}
	for _, c := range x.Devices.Channels {
		if c.Target != nil && c.Target.VirtIO != nil && c.Target.VirtIO.Name == "org.qemu.guest_agent.0" {
			return true
		}
	}
	return false
}

// RestoreVM creates a VM from a backup. If the original VM (same UUID) no
// longer exists and the name is unchanged, UUID and MAC addresses are kept
// (disaster recovery); otherwise the restored VM gets a new identity.
func (m *Manager) RestoreVM(ctx context.Context, store, id string, r agentapi.Restore, rep Reporter) (*agentapi.VMDetail, error) {
	man, dir, err := backup.Read(store, id)
	if err != nil {
		return nil, err
	}
	if man.Kind != "vm" {
		return nil, fmt.Errorf("%w: not a VM backup", ErrConflict)
	}
	if r.Name == "" {
		r.Name = man.Name
	}
	if !agentapi.ValidName(r.Name) || !agentapi.ValidName(r.Pool) {
		return nil, &agentapi.FieldError{Field: "name", Reason: "invalid name or pool"}
	}
	l, err := m.conn()
	if err != nil {
		return nil, err
	}
	if _, err := l.DomainLookupByName(r.Name); err == nil {
		return nil, &agentapi.FieldError{Field: "name", Reason: "a VM with this name already exists"}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "domain.xml"))
	if err != nil {
		return nil, err
	}
	var x libvirtxml.Domain
	if err := x.Unmarshal(string(raw)); err != nil {
		return nil, fmt.Errorf("corrupt domain.xml: %w", err)
	}
	keepIdentity := false
	if r.Name == man.Name {
		var u libvirt.UUID
		if parseUUID(x.UUID, &u) == nil {
			if _, err := l.DomainLookupByUUID(u); err != nil {
				keepIdentity = true
			}
		}
	}
	pool, err := l.StoragePoolLookupByName(r.Pool)
	if err != nil {
		return nil, wrap(err)
	}
	format, ext, err := m.volumeFormat(l, pool)
	if err != nil {
		return nil, err
	}
	var created []libvirt.StorageVol
	cleanup := func() {
		for _, v := range created {
			_ = l.StorageVolDelete(v, 0)
		}
	}
	newPath := map[string]string{}
	var total, before int64
	for _, d := range man.Disks {
		total += int64(d.Capacity)
	}
	for i, d := range man.Disks {
		src := filepath.Join(dir, d.File)
		if filepath.Dir(src) != filepath.Clean(dir) {
			cleanup()
			return nil, fmt.Errorf("corrupt manifest: invalid file %q", d.File)
		}
		volName := fmt.Sprintf("%s-disk%d%s", r.Name, i, ext)
		vol, err := createVolume(l, pool, volName, format, d.Capacity)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("create %s: %w", volName, err)
		}
		created = append(created, vol)
		path, err := l.StorageVolGetPath(vol)
		if err != nil {
			cleanup()
			return nil, wrap(err)
		}
		rep.Say(fmt.Sprintf("Restoring %s to %s", d.Target, path))
		cap := d.Capacity
		err = qemuImgConvert(ctx, []string{"convert", "-p", "-n", "-f", "qcow2", "-O", format, src, path}, func(p float64) {
			rep.Report(before+int64(p/100*float64(cap)), total)
		})
		if err != nil {
			cleanup()
			return nil, err
		}
		before += int64(d.Capacity)
		newPath[d.Target] = path
	}
	x.Name = r.Name
	if !keepIdentity {
		x.UUID = newUUID()
	}
	if x.Devices != nil {
		for i := range x.Devices.Disks {
			dk := &x.Devices.Disks[i]
			if dk.Target == nil {
				continue
			}
			if p, ok := newPath[dk.Target.Dev]; ok {
				dk.Source = diskSource(p)
				if dk.Driver != nil {
					dk.Driver.Type = format
				}
				dk.BackingStore = nil
			} else if dk.Device == "cdrom" {
				if p := diskPath(*dk); p != "" {
					if _, err := os.Stat(p); err != nil {
						dk.Source = nil // the medium no longer exists
					}
				}
			}
		}
		if !keepIdentity {
			for i := range x.Devices.Interfaces {
				x.Devices.Interfaces[i].MAC = nil
			}
		}
		for i := range x.Devices.Interfaces {
			x.Devices.Interfaces[i].Target = nil
		}
	}
	if x.OS != nil {
		x.OS.NVRam = nil
		if man.NVRAM != "" {
			nv := filepath.Join("/var/lib/libvirt/qemu/nvram", r.Name+"_VARS.fd")
			if _, err := os.Stat(nv); err == nil {
				cleanup()
				return nil, fmt.Errorf("%w: %s already exists", ErrConflict, nv)
			}
			if err := os.MkdirAll(filepath.Dir(nv), 0o755); err == nil {
				if err := copyFile(filepath.Join(dir, man.NVRAM), nv); err == nil {
					x.OS.NVRam = &libvirtxml.DomainNVRam{NVRam: nv}
					rep.Say("UEFI variables restored")
				}
			}
		}
	}
	out, err := x.Marshal()
	if err != nil {
		cleanup()
		return nil, err
	}
	d, err := l.DomainDefineXML(out)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("define VM: %w", wrap(err))
	}
	if keepIdentity {
		rep.Say("Original UUID and MAC addresses kept")
	}
	if r.Start {
		rep.Say("Starting " + r.Name)
		if err := l.DomainCreate(d); err != nil {
			return nil, fmt.Errorf("VM restored, but start failed: %w", wrap(err))
		}
	}
	return m.GetVM(r.Name)
}

func parseUUID(s string, u *libvirt.UUID) error {
	h := strings.ReplaceAll(s, "-", "")
	if len(h) != 32 {
		return fmt.Errorf("invalid uuid")
	}
	for i := 0; i < 16; i++ {
		v, err := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if err != nil {
			return err
		}
		u[i] = byte(v)
	}
	return nil
}
