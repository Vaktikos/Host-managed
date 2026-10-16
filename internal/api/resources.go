package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vaktikos/host-managed/internal/agentapi"
	"github.com/vaktikos/host-managed/internal/agentclient"
	"github.com/vaktikos/host-managed/internal/fetch"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/store"
)

func (s *Server) node(w http.ResponseWriter, r *http.Request) {
	h, err := s.Agent.Host(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	v, _ := s.Store.SchemaVersion(r.Context())
	writeJSON(w, 200, map[string]any{"host": h, "control_plane": map[string]any{
		"version": s.Version, "schema_version": v, "allow_software_emulation": s.Cfg.AllowSoftwareEmulation,
	}})
}

func (s *Server) interfaces(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Interfaces(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) networks(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Networks(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) pools(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Pools(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) volumes(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Volumes(r.Context(), r.PathValue("pool"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	pool, vol := r.PathValue("pool"), r.PathValue("vol")
	err := s.Agent.DeleteVolume(r.Context(), pool, vol)
	s.audit(r, "storage.volume.delete", "storage/"+pool+"/"+vol, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upload accepts the raw file as request body (no multipart) so large ISO
// images stream straight to disk.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	pool, name := r.PathValue("pool"), r.PathValue("name")
	if !agentapi.ValidName(pool) || !agentapi.ValidVolumeName(name) {
		valErr(w, r, "name", "invalid file name")
		return
	}
	limit := s.Cfg.MaxUploadGiB << 30
	if r.ContentLength < 0 {
		s.fail(w, r, http.StatusLengthRequired, "length_required", "Content-Length is required")
		return
	}
	if r.ContentLength > limit {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("file exceeds the upload limit of %d GiB", s.Cfg.MaxUploadGiB))
		return
	}
	// Uploads can take long; lift the server's write deadline for this request.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	err := s.Agent.Upload(r.Context(), pool, name, r.ContentLength, r.Body)
	s.audit(r, "storage.upload", "storage/"+pool+"/"+name, err, strconv.FormatInt(r.ContentLength, 10)+" bytes")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 201, map[string]any{"pool": pool, "name": name, "size": r.ContentLength})
}

// downloadToPool starts a job that fetches a file (e.g. an ISO) from a URL
// into a directory pool on the node.
func (s *Server) downloadToPool(w http.ResponseWriter, r *http.Request) {
	pool := r.PathValue("pool")
	var d agentapi.Download
	if err := decodeBody(w, r, &d); err != nil {
		s.agentFail(w, r, err)
		return
	}
	d.URL = strings.TrimSpace(d.URL)
	if _, err := fetch.ValidateURL(d.URL); err != nil {
		valErr(w, r, "url", err.Error())
		return
	}
	if d.Name == "" {
		d.Name = fetch.FileNameFromURL(d.URL)
	}
	if !agentapi.ValidName(pool) {
		s.fail(w, r, 404, "not_found", "unknown pool")
		return
	}
	if !agentapi.ValidVolumeName(d.Name) {
		valErr(w, r, "name", "invalid file name; letters, digits, '.', '_', '+', '-'")
		return
	}
	if err := fetch.ValidateChecksum(d.Checksum); err != nil {
		valErr(w, r, "checksum", err.Error())
		return
	}
	user := identity(r).User.Username
	target := "storage/" + pool + "/" + d.Name
	j, err := s.Jobs.Submit(r.Context(), "storage.download", target, user, d, func(ctx context.Context, tk jobs.Task) error {
		return s.Agent.Download(ctx, pool, d, tk.Log, tk.Progress)
	})
	u, _ := url.Parse(d.URL)
	s.audit(r, "storage.download", target, err, u.Redacted())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

// vmListItem adds the latest live sample to the summary.
type vmListItem struct {
	agentapi.VM
	Stats *agentapi.VMStats `json:"stats,omitempty"`
}

func (s *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	vms, err := s.Agent.VMs(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	stats, _ := s.Agent.LatestStats(r.Context())
	out := make([]vmListItem, 0, len(vms))
	for _, v := range vms {
		it := vmListItem{VM: v}
		if st, ok := stats[v.Name]; ok {
			st := st
			it.Stats = &st
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

func (s *Server) getVM(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.VM(r.Context(), r.PathValue("name"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func jobAccepted(w http.ResponseWriter, j *store.Job) {
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"job": j})
}

func (s *Server) createVM(w http.ResponseWriter, r *http.Request) {
	var spec agentapi.VMCreate
	if err := decodeBody(w, r, &spec); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := spec.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if _, err := s.Agent.VM(r.Context(), spec.Name); err == nil {
		valErr(w, r, "name", "a VM with this name already exists")
		return
	}
	user := identity(r).User.Username
	// Job parameters are persisted: never store the cloud-init password.
	params := spec
	if spec.CloudInit != nil {
		ci := *spec.CloudInit
		if ci.Password != "" {
			ci.Password = "(redacted)"
		}
		params.CloudInit = &ci
	}
	j, err := s.Jobs.Submit(r.Context(), "vm.create", "vm/"+spec.Name, user, params, func(ctx context.Context, tk jobs.Task) error {
		log := tk.Log
		_, err := s.Agent.CreateVM(ctx, spec, log)
		return err
	})
	s.audit(r, "vm.create", "vm/"+spec.Name, err, fmt.Sprintf("vcpus=%d memory=%dMiB disk=%dGiB pool=%s", spec.VCPUs, spec.MemoryMiB, spec.DiskSizeGiB, spec.DiskPool))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) deleteVM(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	disks := r.URL.Query().Get("delete_disks") == "true"
	// Deletion needs an explicit confirmation carrying the VM name.
	if r.URL.Query().Get("confirm") != name {
		valErr(w, r, "confirm", "type the VM name to confirm deletion")
		return
	}
	vm, err := s.Agent.VM(r.Context(), name)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	if vm.State != "shutoff" {
		s.fail(w, r, 409, "conflict", "VM must be shut off before deletion")
		return
	}
	user := identity(r).User.Username
	j, err := s.Jobs.Submit(r.Context(), "vm.delete", "vm/"+name, user, map[string]any{"delete_disks": disks}, func(ctx context.Context, tk jobs.Task) error {
		log := tk.Log
		removed, warning, err := s.Agent.DeleteVM(ctx, name, disks)
		for _, d := range removed {
			log("Removed disk " + d)
		}
		if warning != "" {
			return fmt.Errorf("%s", warning)
		}
		return err
	})
	s.audit(r, "vm.delete", "vm/"+name, err, "delete_disks="+strconv.FormatBool(disks))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) vmAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !agentapi.ValidVMAction(action) {
		s.fail(w, r, 404, "not_found", "unknown action")
		return
	}
	err := s.Agent.VMAction(r.Context(), name, action)
	s.audit(r, "vm."+action, "vm/"+name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmMeta(w http.ResponseWriter, r *http.Request) {
	var m agentapi.VMMeta
	if err := decodeBody(w, r, &m); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := m.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	name := r.PathValue("name")
	err := s.Agent.VMMeta(r.Context(), name, m)
	s.audit(r, "vm.meta", "vm/"+name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmResources(w http.ResponseWriter, r *http.Request) {
	var res agentapi.VMResources
	if err := decodeBody(w, r, &res); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := res.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	name := r.PathValue("name")
	pending, err := s.Agent.VMResources(r.Context(), name, res)
	s.audit(r, "vm.resources", "vm/"+name, err, fmt.Sprintf("vcpus=%d memory=%dMiB", res.VCPUs, res.MemoryMiB))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"pending_restart": pending})
}

func (s *Server) vmTemplate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Template bool `json:"template"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		s.agentFail(w, r, err)
		return
	}
	name := r.PathValue("name")
	err := s.Agent.VMTemplate(r.Context(), name, body.Template)
	s.audit(r, "vm.template", "vm/"+name, err, "template="+strconv.FormatBool(body.Template))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vmStats(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.VMStats(r.Context(), r.PathValue("name"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) vmGuest(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.VMGuest(r.Context(), r.PathValue("name"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) listCTs(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Containers(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) getCT(w http.ResponseWriter, r *http.Request) {
	v, err := s.Agent.Container(r.Context(), r.PathValue("name"))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) createCT(w http.ResponseWriter, r *http.Request) {
	var spec agentapi.ContainerCreate
	if err := decodeBody(w, r, &spec); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if err := spec.Validate(); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if _, err := s.Agent.Container(r.Context(), spec.Name); err == nil {
		valErr(w, r, "name", "a container with this name already exists")
		return
	}
	user := identity(r).User.Username
	j, err := s.Jobs.Submit(r.Context(), "ct.create", "ct/"+spec.Name, user, spec, func(ctx context.Context, tk jobs.Task) error {
		log := tk.Log
		_, err := s.Agent.CreateContainer(ctx, spec, log)
		return err
	})
	s.audit(r, "ct.create", "ct/"+spec.Name, err, spec.Distribution+"/"+spec.Release)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) deleteCT(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if r.URL.Query().Get("confirm") != name {
		valErr(w, r, "confirm", "type the container name to confirm deletion")
		return
	}
	user := identity(r).User.Username
	j, err := s.Jobs.Submit(r.Context(), "ct.delete", "ct/"+name, user, nil, func(ctx context.Context, tk jobs.Task) error {
		return s.Agent.DeleteContainer(ctx, name)
	})
	s.audit(r, "ct.delete", "ct/"+name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	jobAccepted(w, j)
}

func (s *Server) ctAction(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !agentapi.ValidContainerAction(action) {
		s.fail(w, r, 404, "not_found", "unknown action")
		return
	}
	err := s.Agent.ContainerAction(r.Context(), name, action)
	s.audit(r, "ct."+action, "ct/"+name, err, "")
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) ctConfig(w http.ResponseWriter, r *http.Request) {
	var cfg agentclient.ContainerConfig
	if err := decodeBody(w, r, &cfg); err != nil {
		s.agentFail(w, r, err)
		return
	}
	if cfg.MemoryMiB != 0 && cfg.MemoryMiB < 32 {
		valErr(w, r, "memory_mib", "at least 32 MiB or 0 for unlimited")
		return
	}
	if cfg.CPULimit < 0 || cfg.CPULimit > 512 {
		valErr(w, r, "cpu_limit", "must be between 0 and 512")
		return
	}
	name := r.PathValue("name")
	err := s.Agent.ContainerConfig(r.Context(), name, cfg)
	s.audit(r, "ct.config", "ct/"+name, err, fmt.Sprintf("memory=%dMiB cpu=%g", cfg.MemoryMiB, cfg.CPULimit))
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) lxcImages(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	if arch != "amd64" && arch != "arm64" && arch != "i386" {
		valErr(w, r, "arch", "unsupported architecture")
		return
	}
	v, err := s.Agent.LXCImages(r.Context(), arch)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, v)
}

func intParam(r *http.Request, key string, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil || n < 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, total, err := s.Store.Jobs(r.Context(), store.JobFilter{
		Target: q.Get("target"), Status: q.Get("status"),
		Limit: intParam(r, "limit", 50, 500), Offset: intParam(r, "offset", 0, 1<<30),
	})
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list, "total": total})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Store.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, 404, "not_found", "job not found")
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	lines, err := s.Store.JobLog(r.Context(), r.PathValue("id"), after)
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, lines)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	j, err := s.Store.Job(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, 404, "not_found", "job not found")
		return
	}
	if j.Username != id.User.Username && id.User.Role != "admin" {
		s.fail(w, r, 403, "forbidden", "only the initiator or an administrator can cancel this job")
		return
	}
	ok := s.Jobs.Cancel(j.ID)
	s.audit(r, "job.cancel", "job/"+j.ID, nil, "")
	if !ok {
		s.fail(w, r, 409, "conflict", "job is not running")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, total, err := s.Store.AuditLog(r.Context(), store.AuditFilter{
		Username: q.Get("user"), Action: q.Get("action"), Resource: q.Get("resource"),
		Limit: intParam(r, "limit", 100, 500), Offset: intParam(r, "offset", 0, 1<<30),
	})
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list, "total": total})
}

func (s *Server) auditVerify(w http.ResponseWriter, r *http.Request) {
	n, broken, err := s.Store.VerifyAudit(r.Context())
	if err != nil {
		s.agentFail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"checked": n, "intact": broken == 0, "broken_at": broken})
}
