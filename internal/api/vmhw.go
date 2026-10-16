package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/jobs"
)

// change runs a synchronous hardware change, audits it and returns the
// result (pending_restart tells the UI whether a restart is needed).
func (s *Server) change(w http.ResponseWriter, r *http.Request, action, detail string, fn func(ctx context.Context) (*agentapi.ChangeResult, error)) {
	name := r.PathValue("name")
	res, err := fn(r.Context())
	s.audit(r, action, "vm/"+name, err, detail)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	if res == nil {
		res = &agentapi.ChangeResult{}
	}
	writeJSON(w, 200, res)
}

func (s *Server) vmAddDisk(w http.ResponseWriter, r *http.Request) {
	var d agentapi.DiskAdd
	if err := decodeBody(w, r, &d); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := d.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	s.change(w, r, "vm.disk.add", fmt.Sprintf("pool=%s size=%dGiB bus=%s", d.Pool, d.SizeGiB, d.Bus), func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMAddDisk(ctx, r.PathValue("name"), d)
	})
}

func (s *Server) vmRemoveDisk(w http.ResponseWriter, r *http.Request) {
	target := r.PathValue("target")
	del := r.URL.Query().Get("delete_volume") == "true"
	if del {
		// Deleting disk data is destructive: needs vm.delete and the target
		// typed as confirmation.
		if !auth.Allowed(identity(r).User.Role, auth.PermVMDelete) {
			s.fail(w, r, 403, "forbidden", "missing permission: "+auth.PermVMDelete)
			return
		}
		if r.URL.Query().Get("confirm") != target {
			valErr(w, r, "confirm", "type the disk name to confirm deleting its data")
			return
		}
	}
	s.change(w, r, "vm.disk.remove", fmt.Sprintf("target=%s delete_volume=%v", target, del), func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMRemoveDisk(ctx, r.PathValue("name"), target, del)
	})
}

func (s *Server) vmResizeDisk(w http.ResponseWriter, r *http.Request) {
	var d agentapi.DiskResize
	if err := decodeBody(w, r, &d); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := d.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	target := r.PathValue("target")
	s.change(w, r, "vm.disk.resize", fmt.Sprintf("target=%s size=%dGiB", target, d.SizeGiB), func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMResizeDisk(ctx, r.PathValue("name"), target, d)
	})
}

func (s *Server) vmMedia(w http.ResponseWriter, r *http.Request) {
	var md agentapi.Media
	if err := decodeBody(w, r, &md); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := md.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	detail := "eject"
	if md.Volume != "" {
		detail = md.Pool + "/" + md.Volume
	}
	s.change(w, r, "vm.media", r.PathValue("target")+" "+detail, func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return nil, s.Agent.VMMedia(ctx, r.PathValue("name"), r.PathValue("target"), md)
	})
}

func (s *Server) vmAddNIC(w http.ResponseWriter, r *http.Request) {
	var n agentapi.NICAdd
	if err := decodeBody(w, r, &n); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := n.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	s.change(w, r, "vm.nic.add", n.Network+" "+n.Model, func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMAddNIC(ctx, r.PathValue("name"), n)
	})
}

func (s *Server) vmRemoveNIC(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(r.PathValue("mac"))
	s.change(w, r, "vm.nic.remove", mac, func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMRemoveNIC(ctx, r.PathValue("name"), mac)
	})
}

func (s *Server) vmBoot(w http.ResponseWriter, r *http.Request) {
	var b agentapi.BootOrder
	if err := decodeBody(w, r, &b); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := b.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	s.change(w, r, "vm.boot", strings.Join(b.Order, ","), func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return s.Agent.VMBootOrder(ctx, r.PathValue("name"), b)
	})
}

func (s *Server) vmSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.Agent.VMSnapshots(r.Context(), r.PathValue("name"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, list)
}

// Snapshot operations run as jobs: saving or restoring memory state takes
// as long as writing the VM's RAM to disk.
func (s *Server) snapshotJob(w http.ResponseWriter, r *http.Request, typ, detail string, fn func(ctx context.Context, tk jobs.Task) error) {
	name := r.PathValue("name")
	j, err := s.Jobs.Submit(r.Context(), typ, "vm/"+name, identity(r).User.Username, map[string]string{"detail": detail}, fn)
	s.audit(r, typ, "vm/"+name, err, detail)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) vmCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var c agentapi.SnapshotCreate
	if err := decodeBody(w, r, &c); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := c.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	name := r.PathValue("name")
	s.snapshotJob(w, r, "vm.snapshot.create", c.Name, func(ctx context.Context, tk jobs.Task) error {
		tk.Log("Creating snapshot " + c.Name)
		sn, err := s.Agent.VMCreateSnapshot(ctx, name, c)
		if err == nil && sn.Memory {
			tk.Log("Snapshot includes the memory state")
		}
		return err
	})
}

func (s *Server) vmRevertSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := r.PathValue("snap")
	if !agentapi.ValidSnapshotName(snap) {
		s.fail(w, r, 404, "not_found", "snapshot not found")
		return
	}
	name := r.PathValue("name")
	s.snapshotJob(w, r, "vm.snapshot.revert", snap, func(ctx context.Context, tk jobs.Task) error {
		tk.Log("Reverting to snapshot " + snap)
		return s.Agent.VMRevertSnapshot(ctx, name, snap)
	})
}

func (s *Server) vmDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := r.PathValue("snap")
	if !agentapi.ValidSnapshotName(snap) {
		s.fail(w, r, 404, "not_found", "snapshot not found")
		return
	}
	name := r.PathValue("name")
	s.snapshotJob(w, r, "vm.snapshot.delete", snap, func(ctx context.Context, tk jobs.Task) error {
		tk.Log("Deleting snapshot " + snap)
		return s.Agent.VMDeleteSnapshot(ctx, name, snap)
	})
}

func (s *Server) vmClone(w http.ResponseWriter, r *http.Request) {
	var c agentapi.Clone
	if err := decodeBody(w, r, &c); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := c.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if _, err := s.Agent.VM(r.Context(), c.Name); err == nil {
		valErr(w, r, "name", "a VM with this name already exists")
		return
	}
	src := r.PathValue("name")
	j, err := s.Jobs.Submit(r.Context(), "vm.clone", "vm/"+c.Name, identity(r).User.Username, c, func(ctx context.Context, tk jobs.Task) error {
		_, err := s.Agent.VMClone(ctx, src, c, tk.Log)
		return err
	})
	s.audit(r, "vm.clone", "vm/"+src, err, "to="+c.Name+" mode="+c.Mode)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) vmCloudInit(w http.ResponseWriter, r *http.Request) {
	var ci agentapi.CloudInit
	if err := decodeBody(w, r, &ci); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if ci.Hostname == "" {
		ci.Hostname = r.PathValue("name")
	}
	// "set" keeps the stored password hash; validate a copy accordingly.
	check := ci
	if check.PasswordHash == "set" {
		check.PasswordHash = ""
		if check.Password == "" {
			check.Password = "placeholder-only"
		}
	}
	if err := check.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	s.change(w, r, "vm.cloudinit", "user="+ci.User+" hostname="+ci.Hostname, func(ctx context.Context) (*agentapi.ChangeResult, error) {
		return &agentapi.ChangeResult{PendingRestart: true}, s.Agent.VMCloudInit(ctx, r.PathValue("name"), ci)
	})
}
