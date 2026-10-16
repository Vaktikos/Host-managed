// Command hostctl manages a Host-managed installation through its REST API.
package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

var version = "dev"

type cli struct {
	base   string
	token  string
	json   bool
	hc     *http.Client
	german bool
}

func main() {
	c := &cli{german: isGerman()}
	fs := flag.NewFlagSet("hostctl", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	urlFlag := fs.String("url", os.Getenv("HM_URL"), "")
	tokFlag := fs.String("token", os.Getenv("HM_TOKEN"), "")
	fpFlag := fs.String("fingerprint", os.Getenv("HM_FINGERPRINT"), "")
	caFlag := fs.String("ca-cert", os.Getenv("HM_CA_CERT"), "")
	fs.BoolVar(&c.json, "json", false, "")
	if err := fs.Parse(os.Args[1:]); err != nil || fs.NArg() == 0 {
		c.usage()
		os.Exit(2)
	}
	args := fs.Args()
	if args[0] == "version" {
		fmt.Println(version)
		return
	}
	if args[0] == "help" || args[0] == "-h" {
		c.usage()
		return
	}
	if *urlFlag == "" || *tokFlag == "" {
		c.fatal(c.msg("missing --url/HM_URL or --token/HM_TOKEN", "--url/HM_URL oder --token/HM_TOKEN fehlt"))
	}
	c.base = strings.TrimRight(*urlFlag, "/")
	c.token = *tokFlag
	tr, err := transport(*fpFlag, *caFlag)
	if err != nil {
		c.fatal(err.Error())
	}
	c.hc = &http.Client{Timeout: 60 * time.Second, Transport: tr}
	if err := c.run(args); err != nil {
		c.fatal(err.Error())
	}
}

func isGerman() bool {
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if v != "" {
			return strings.HasPrefix(v, "de")
		}
	}
	return false
}

func (c *cli) msg(en, de string) string {
	if c.german {
		return de
	}
	return en
}

func (c *cli) fatal(s string) {
	fmt.Fprintln(os.Stderr, c.msg("error: ", "Fehler: ")+s)
	os.Exit(1)
}

// transport pins the server certificate by SHA-256 fingerprint or trusts a
// given CA file; otherwise the system trust store is used.
func transport(fingerprint, caFile string) (*http.Transport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	switch {
	case fingerprint != "":
		want := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(fingerprint, "sha256:"), ":", ""))
		if len(want) != 64 {
			return nil, errors.New("fingerprint must be a SHA-256 hex digest")
		}
		cfg.InsecureSkipVerify = true // replaced by the pin check below
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no server certificate")
			}
			sum := sha256.Sum256(raw[0])
			if hex.EncodeToString(sum[:]) != want {
				return errors.New("server certificate fingerprint does not match")
			}
			return nil
		}
	case caFile != "":
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("no certificates in " + caFile)
		}
		cfg.RootCAs = pool
	}
	tr.TLSClientConfig = cfg
	return tr, nil
}

func (c *cli) usage() {
	if c.german {
		fmt.Fprint(os.Stderr, `Verwendung: hostctl [--url URL] [--token TOKEN] [--fingerprint SHA256] [--json] <Befehl>

Verbindung (auch über Umgebungsvariablen):
  HM_URL          z. B. https://host:8443
  HM_TOKEN        API-Token (Weboberfläche: Konto → API-Tokens)
  HM_FINGERPRINT  SHA-256-Fingerprint des Serverzertifikats (selbstsigniert)
  HM_CA_CERT      CA-Datei statt Fingerprint

Befehle:
  node show
  vm list | show NAME | start|shutdown|stop|reboot|reset|pause|resume NAME
  vm create --name N --pool P [--vcpus 2] [--memory 2048] [--disk 32] [--network network:default]
            [--iso POOL/DATEI | --image POOL/DATEI --ci-user U --ci-ssh-key DATEI [--ci-ip CIDR --ci-gw IP]]
            [--firmware uefi|bios] [--start] [--wait]
  vm delete NAME --yes [--delete-disks] [--wait]
  vm clone VM NEWNAME [--linked] [--start] [--wait]
  vm snapshot list|create|revert|delete VM [NAME] [--wait]
  vm disk add VM --size GIB [--pool P] [--bus virtio|scsi|sata] | resize VM TARGET GIB | remove VM TARGET [--delete-data --yes]
  vm nic add VM network:<name>|bridge:<name> | remove VM MAC
  vm cdrom insert VM TARGET POOL/FILE | eject VM TARGET
  ct list | show NAME | start|shutdown|stop|reboot NAME
  storage list | volumes POOL | download POOL URL [--checksum sha256:HEX] [--wait]
  job list | show ID | log ID | wait ID
  version
`)
		return
	}
	fmt.Fprint(os.Stderr, `Usage: hostctl [--url URL] [--token TOKEN] [--fingerprint SHA256] [--json] <command>

Connection (also via environment):
  HM_URL          e.g. https://host:8443
  HM_TOKEN        API token (web UI: Account → API tokens)
  HM_FINGERPRINT  SHA-256 fingerprint of the server certificate (self-signed)
  HM_CA_CERT      CA file instead of a fingerprint

Commands:
  node show
  vm list | show NAME | start|shutdown|stop|reboot|reset|pause|resume NAME
  vm create --name N --pool P [--vcpus 2] [--memory 2048] [--disk 32] [--network network:default]
            [--iso POOL/FILE | --image POOL/FILE --ci-user U --ci-ssh-key FILE [--ci-ip CIDR --ci-gw IP]]
            [--firmware uefi|bios] [--start] [--wait]
  vm delete NAME --yes [--delete-disks] [--wait]
  vm clone VM NEWNAME [--linked] [--start] [--wait]
  vm snapshot list|create|revert|delete VM [NAME] [--wait]
  vm disk add VM --size GIB [--pool P] [--bus virtio|scsi|sata] | resize VM TARGET GIB | remove VM TARGET [--delete-data --yes]
  vm nic add VM network:<name>|bridge:<name> | remove VM MAC
  vm cdrom insert VM TARGET POOL/FILE | eject VM TARGET
  ct list | show NAME | start|shutdown|stop|reboot NAME
  storage list | volumes POOL | download POOL URL [--checksum sha256:HEX] [--wait]
  job list | show ID | log ID | wait ID
  version
`)
}

type apiErr struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Field     string `json:"field"`
		RequestID string `json:"request_id"`
	} `json:"error"`
}

func (c *cli) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+"/api/v1"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 400 {
		var e apiErr
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			m := e.Error.Message
			if e.Error.Field != "" {
				m = e.Error.Field + ": " + m
			}
			return fmt.Errorf("%s (%s, HTTP %d)", m, e.Error.Code, resp.StatusCode)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *cli) printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func table(header []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintln(w, strings.Join(r, "\t"))
	}
	w.Flush()
}

func human(n float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for n >= 1024 && i < len(units)-1 {
		n /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", n, units[i])
}

func need(args []string, n int, usage string) error {
	if len(args) < n {
		return errors.New("usage: hostctl " + usage)
	}
	return nil
}

func (c *cli) run(args []string) error {
	switch args[0] {
	case "node":
		return c.node(args[1:])
	case "vm":
		return c.vm(args[1:])
	case "ct":
		return c.ct(args[1:])
	case "storage":
		return c.storage(args[1:])
	case "job":
		return c.job(args[1:])
	}
	c.usage()
	return fmt.Errorf("unknown command %q", args[0])
}

func (c *cli) node(args []string) error {
	var n map[string]any
	if err := c.do("GET", "/node", nil, &n); err != nil {
		return err
	}
	if c.json {
		c.printJSON(n)
		return nil
	}
	h, _ := n["host"].(map[string]any)
	fmt.Printf("hostname   %v\nos         %v\nkernel     %v\ncpu        %v (%v threads)\nkvm        %v\nmemory     %s / %s\nlibvirt    %v\nqemu       %v\n",
		h["hostname"], h["os"], h["kernel"], h["cpu_model"], h["cpu_threads"], h["kvm_available"],
		human(h["mem_total"].(float64)-h["mem_available"].(float64)), human(h["mem_total"].(float64)), h["libvirt_version"], h["qemu_version"])
	return nil
}

var vmActions = map[string]bool{"start": true, "shutdown": true, "stop": true, "reboot": true, "reset": true, "pause": true, "resume": true}

func (c *cli) vm(args []string) error {
	if err := need(args, 1, "vm list|show|create|delete|<action>"); err != nil {
		return err
	}
	switch sub := args[0]; {
	case sub == "list":
		var vms []map[string]any
		if err := c.do("GET", "/vms", nil, &vms); err != nil {
			return err
		}
		if c.json {
			c.printJSON(vms)
			return nil
		}
		sort.Slice(vms, func(i, j int) bool { return fmt.Sprint(vms[i]["name"]) < fmt.Sprint(vms[j]["name"]) })
		var rows [][]string
		for _, v := range vms {
			cpu := "-"
			if st, ok := v["stats"].(map[string]any); ok && st["cpu_usage"] != nil {
				cpu = fmt.Sprintf("%.1f%%", st["cpu_usage"].(float64))
			}
			rows = append(rows, []string{fmt.Sprint(v["name"]), fmt.Sprint(v["state"]), fmt.Sprint(v["vcpus"]), fmt.Sprintf("%v MiB", v["memory_mib"]), cpu})
		}
		table([]string{"NAME", "STATE", "VCPUS", "MEMORY", "CPU"}, rows)
	case sub == "show":
		if err := need(args, 2, "vm show NAME"); err != nil {
			return err
		}
		var v map[string]any
		if err := c.do("GET", "/vms/"+url.PathEscape(args[1]), nil, &v); err != nil {
			return err
		}
		c.printJSON(v)
	case vmActions[sub]:
		if err := need(args, 2, "vm "+sub+" NAME"); err != nil {
			return err
		}
		if err := c.do("POST", "/vms/"+url.PathEscape(args[1])+"/actions/"+sub, nil, nil); err != nil {
			return err
		}
		fmt.Println(c.msg("ok", "erledigt"))
	case sub == "create":
		return c.vmCreate(args[1:])
	case sub == "snapshot":
		return c.vmSnapshot(args[1:])
	case sub == "clone":
		return c.vmClone(args[1:])
	case sub == "disk":
		return c.vmDisk(args[1:])
	case sub == "nic":
		return c.vmNIC(args[1:])
	case sub == "cdrom":
		return c.vmCDROM(args[1:])
	case sub == "delete":
		fs := flag.NewFlagSet("vm delete", flag.ContinueOnError)
		yes := fs.Bool("yes", false, "confirm deletion")
		disks := fs.Bool("delete-disks", false, "also delete disks")
		wait := fs.Bool("wait", false, "wait for the job")
		if err := need(args, 2, "vm delete NAME --yes [--delete-disks]"); err != nil {
			return err
		}
		name := args[1]
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if !*yes {
			return errors.New(c.msg("refusing to delete without --yes", "Löschen nur mit --yes"))
		}
		var res struct {
			Job struct{ ID string } `json:"job"`
		}
		q := url.Values{"confirm": {name}, "delete_disks": {fmt.Sprint(*disks)}}
		if err := c.do("DELETE", "/vms/"+url.PathEscape(name)+"?"+q.Encode(), nil, &res); err != nil {
			return err
		}
		return c.jobResult(res.Job.ID, *wait)
	default:
		return fmt.Errorf("unknown vm command %q", sub)
	}
	return nil
}

func (c *cli) vmCreate(args []string) error {
	fs := flag.NewFlagSet("vm create", flag.ContinueOnError)
	name := fs.String("name", "", "VM name")
	pool := fs.String("pool", "local", "storage pool for the disk")
	vcpus := fs.Int("vcpus", 2, "vCPUs")
	mem := fs.Int("memory", 2048, "memory in MiB")
	disk := fs.Int("disk", 32, "disk size in GiB")
	network := fs.String("network", "network:default", "network:<name> or bridge:<name>")
	iso := fs.String("iso", "", "installation ISO as POOL/FILE")
	fw := fs.String("firmware", "uefi", "uefi or bios")
	start := fs.Bool("start", false, "start after creation")
	wait := fs.Bool("wait", false, "wait for the job")
	desc := fs.String("description", "", "description")
	image := fs.String("image", "", "disk image (cloud image) as POOL/FILE instead of an empty disk")
	ciUser := fs.String("ci-user", "", "cloud-init: user name (enables cloud-init)")
	ciKey := fs.String("ci-ssh-key", "", "cloud-init: public key file")
	ciPwStdin := fs.Bool("ci-password-stdin", false, "cloud-init: read password from stdin")
	ciIP := fs.String("ci-ip", "", "cloud-init: static IPv4 CIDR (default DHCP)")
	ciGW := fs.String("ci-gw", "", "cloud-init: IPv4 gateway")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body := map[string]any{
		"name": *name, "description": *desc, "tags": []string{}, "vcpus": *vcpus, "memory_mib": *mem,
		"cpu_mode": "host-passthrough", "firmware": *fw, "disk_pool": *pool, "disk_size_gib": *disk,
		"disk_bus": "virtio", "network": *network, "nic_model": "virtio", "guest_agent": true, "start": *start,
	}
	if *iso != "" {
		p, f, ok := strings.Cut(*iso, "/")
		if !ok {
			return errors.New("--iso expects POOL/FILE")
		}
		body["iso_pool"], body["iso_volume"] = p, f
	}
	if *image != "" {
		p, f, ok := strings.Cut(*image, "/")
		if !ok {
			return errors.New("--image expects POOL/FILE")
		}
		body["image_pool"], body["image_volume"] = p, f
	}
	if *ciUser != "" {
		ci := map[string]any{"user": *ciUser, "guest_agent": true, "ipv4": *ciIP, "gateway4": *ciGW}
		if *ciKey != "" {
			b, err := os.ReadFile(*ciKey)
			if err != nil {
				return err
			}
			ci["ssh_keys"] = []string{strings.TrimSpace(string(b))}
		}
		if *ciPwStdin {
			b, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
			if err != nil {
				return err
			}
			ci["password"] = strings.TrimRight(string(b), "\r\n")
		}
		body["cloud_init"] = ci
	}
	var res struct {
		Job struct{ ID string } `json:"job"`
	}
	if err := c.do("POST", "/vms", body, &res); err != nil {
		return err
	}
	return c.jobResult(res.Job.ID, *wait)
}

func (c *cli) jobResult(id string, wait bool) error {
	if !wait {
		fmt.Println(id)
		return nil
	}
	return c.waitJob(id)
}

func (c *cli) waitJob(id string) error {
	var after int64
	for {
		var lines []struct {
			ID   int64  `json:"id"`
			Line string `json:"line"`
		}
		if err := c.do("GET", fmt.Sprintf("/jobs/%s/log?after=%d", url.PathEscape(id), after), nil, &lines); err == nil {
			for _, l := range lines {
				fmt.Println(l.Line)
				after = l.ID
			}
		}
		var j map[string]any
		if err := c.do("GET", "/jobs/"+url.PathEscape(id), nil, &j); err != nil {
			return err
		}
		switch j["status"] {
		case "succeeded":
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("job %s %s: %v", id, j["status"], j["error"])
		}
		time.Sleep(time.Second)
	}
}

func (c *cli) ct(args []string) error {
	if err := need(args, 1, "ct list|show|<action>"); err != nil {
		return err
	}
	switch sub := args[0]; sub {
	case "list":
		var cts []map[string]any
		if err := c.do("GET", "/containers", nil, &cts); err != nil {
			return err
		}
		if c.json {
			c.printJSON(cts)
			return nil
		}
		var rows [][]string
		for _, ct := range cts {
			ips := []string{}
			if l, ok := ct["ips"].([]any); ok {
				for _, ip := range l {
					ips = append(ips, fmt.Sprint(ip))
				}
			}
			rows = append(rows, []string{fmt.Sprint(ct["name"]), fmt.Sprint(ct["state"]), strings.Join(ips, ","), fmt.Sprintf("%v %v", ct["distribution"], ct["release"])})
		}
		table([]string{"NAME", "STATE", "IPS", "OS"}, rows)
	case "show":
		if err := need(args, 2, "ct show NAME"); err != nil {
			return err
		}
		var v map[string]any
		if err := c.do("GET", "/containers/"+url.PathEscape(args[1]), nil, &v); err != nil {
			return err
		}
		c.printJSON(v)
	case "start", "shutdown", "stop", "reboot", "freeze", "unfreeze":
		if err := need(args, 2, "ct "+sub+" NAME"); err != nil {
			return err
		}
		if err := c.do("POST", "/containers/"+url.PathEscape(args[1])+"/actions/"+sub, nil, nil); err != nil {
			return err
		}
		fmt.Println(c.msg("ok", "erledigt"))
	default:
		return fmt.Errorf("unknown ct command %q", sub)
	}
	return nil
}

func (c *cli) storage(args []string) error {
	if err := need(args, 1, "storage list|volumes POOL"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var pools []map[string]any
		if err := c.do("GET", "/storage", nil, &pools); err != nil {
			return err
		}
		if c.json {
			c.printJSON(pools)
			return nil
		}
		var rows [][]string
		for _, p := range pools {
			rows = append(rows, []string{fmt.Sprint(p["name"]), fmt.Sprint(p["type"]), fmt.Sprint(p["state"]), human(p["allocation"].(float64)), human(p["capacity"].(float64)), fmt.Sprint(p["path"])})
		}
		table([]string{"NAME", "TYPE", "STATE", "USED", "SIZE", "PATH"}, rows)
	case "volumes":
		if err := need(args, 2, "storage volumes POOL"); err != nil {
			return err
		}
		var vols []map[string]any
		if err := c.do("GET", "/storage/"+url.PathEscape(args[1])+"/volumes", nil, &vols); err != nil {
			return err
		}
		if c.json {
			c.printJSON(vols)
			return nil
		}
		var rows [][]string
		for _, v := range vols {
			rows = append(rows, []string{fmt.Sprint(v["name"]), fmt.Sprint(v["format"]), human(v["capacity"].(float64)), human(v["allocation"].(float64))})
		}
		table([]string{"NAME", "FORMAT", "SIZE", "ALLOCATED"}, rows)
	case "download":
		if err := need(args, 3, "storage download POOL URL [--name FILE] [--checksum sha256:HEX] [--wait]"); err != nil {
			return err
		}
		fs := flag.NewFlagSet("storage download", flag.ContinueOnError)
		name := fs.String("name", "", "file name (default: from URL)")
		sum := fs.String("checksum", "", "sha256:<hex> or sha512:<hex>")
		wait := fs.Bool("wait", false, "wait for the job")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		var res struct {
			Job struct{ ID string } `json:"job"`
		}
		body := map[string]string{"url": args[2], "name": *name, "checksum": *sum}
		if err := c.do("POST", "/storage/"+url.PathEscape(args[1])+"/download", body, &res); err != nil {
			return err
		}
		return c.jobResult(res.Job.ID, *wait)
	default:
		return fmt.Errorf("unknown storage command %q", args[0])
	}
	return nil
}

func (c *cli) job(args []string) error {
	if err := need(args, 1, "job list|show|log|wait"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var res struct {
			Items []map[string]any `json:"items"`
		}
		if err := c.do("GET", "/jobs?limit=50", nil, &res); err != nil {
			return err
		}
		if c.json {
			c.printJSON(res.Items)
			return nil
		}
		var rows [][]string
		for _, j := range res.Items {
			rows = append(rows, []string{fmt.Sprint(j["id"]), fmt.Sprint(j["type"]), fmt.Sprint(j["target"]), fmt.Sprint(j["status"]), fmt.Sprint(j["username"])})
		}
		table([]string{"ID", "TYPE", "TARGET", "STATUS", "USER"}, rows)
	case "show":
		if err := need(args, 2, "job show ID"); err != nil {
			return err
		}
		var j map[string]any
		if err := c.do("GET", "/jobs/"+url.PathEscape(args[1]), nil, &j); err != nil {
			return err
		}
		c.printJSON(j)
	case "log":
		if err := need(args, 2, "job log ID"); err != nil {
			return err
		}
		var lines []struct {
			Time string `json:"time"`
			Line string `json:"line"`
		}
		if err := c.do("GET", "/jobs/"+url.PathEscape(args[1])+"/log", nil, &lines); err != nil {
			return err
		}
		for _, l := range lines {
			fmt.Printf("%s %s\n", l.Time, l.Line)
		}
	case "wait":
		if err := need(args, 2, "job wait ID"); err != nil {
			return err
		}
		return c.waitJob(args[1])
	default:
		return fmt.Errorf("unknown job command %q", args[0])
	}
	return nil
}

func (c *cli) printChange(res map[string]any) {
	if res["pending_restart"] == true {
		fmt.Println(c.msg("saved; takes effect after the next VM start", "gespeichert; wirksam nach dem nächsten Start der VM"))
		return
	}
	if t, ok := res["target"].(string); ok && t != "" {
		fmt.Println(t)
		return
	}
	fmt.Println(c.msg("ok", "erledigt"))
}

func (c *cli) jobFrom(method, path string, body any, wait bool) error {
	var res struct {
		Job struct{ ID string } `json:"job"`
	}
	if err := c.do(method, path, body, &res); err != nil {
		return err
	}
	return c.jobResult(res.Job.ID, wait)
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func (c *cli) vmSnapshot(args []string) error {
	if err := need(args, 2, "vm snapshot list|create|revert|delete VM [NAME]"); err != nil {
		return err
	}
	base := "/vms/" + url.PathEscape(args[1]) + "/snapshots"
	wait := hasFlag(args, "--wait")
	switch args[0] {
	case "list":
		var list []map[string]any
		if err := c.do("GET", base, nil, &list); err != nil {
			return err
		}
		if c.json {
			c.printJSON(list)
			return nil
		}
		var rows [][]string
		for _, s := range list {
			created := time.Unix(int64(s["created_at"].(float64)), 0).Format(time.RFC3339)
			rows = append(rows, []string{fmt.Sprint(s["name"]), created, fmt.Sprint(s["state"]), fmt.Sprint(s["memory"]), fmt.Sprint(s["current"]), fmt.Sprint(s["description"])})
		}
		table([]string{"NAME", "CREATED", "STATE", "MEMORY", "CURRENT", "DESCRIPTION"}, rows)
		return nil
	case "create":
		if err := need(args, 3, "vm snapshot create VM NAME [--description TEXT] [--wait]"); err != nil {
			return err
		}
		fs := flag.NewFlagSet("snapshot create", flag.ContinueOnError)
		desc := fs.String("description", "", "")
		fs.Bool("wait", false, "")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		return c.jobFrom("POST", base, map[string]string{"name": args[2], "description": *desc}, wait)
	case "revert":
		if err := need(args, 3, "vm snapshot revert VM NAME --yes"); err != nil {
			return err
		}
		if !hasFlag(args, "--yes") {
			return errors.New(c.msg("reverting discards all changes since the snapshot; add --yes", "Zurücksetzen verwirft alle Änderungen seit dem Snapshot; --yes angeben"))
		}
		return c.jobFrom("POST", base+"/"+url.PathEscape(args[2])+"/revert", nil, wait)
	case "delete":
		if err := need(args, 3, "vm snapshot delete VM NAME"); err != nil {
			return err
		}
		return c.jobFrom("DELETE", base+"/"+url.PathEscape(args[2]), nil, wait)
	}
	return fmt.Errorf("unknown snapshot command %q", args[0])
}

func (c *cli) vmClone(args []string) error {
	if err := need(args, 2, "vm clone VM NEWNAME [--linked] [--start] [--wait]"); err != nil {
		return err
	}
	mode := "full"
	if hasFlag(args, "--linked") {
		mode = "linked"
	}
	body := map[string]any{"name": args[1], "mode": mode, "start": hasFlag(args, "--start")}
	return c.jobFrom("POST", "/vms/"+url.PathEscape(args[0])+"/clone", body, hasFlag(args, "--wait"))
}

func (c *cli) vmDisk(args []string) error {
	if err := need(args, 2, "vm disk add|resize|remove VM ..."); err != nil {
		return err
	}
	base := "/vms/" + url.PathEscape(args[1]) + "/disks"
	var res map[string]any
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("disk add", flag.ContinueOnError)
		pool := fs.String("pool", "local", "")
		size := fs.Int("size", 0, "GiB")
		bus := fs.String("bus", "virtio", "")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if err := c.do("POST", base, map[string]any{"pool": *pool, "size_gib": *size, "bus": *bus}, &res); err != nil {
			return err
		}
	case "resize":
		if err := need(args, 4, "vm disk resize VM TARGET SIZE_GIB"); err != nil {
			return err
		}
		var n int
		if _, err := fmt.Sscan(args[3], &n); err != nil {
			return err
		}
		if err := c.do("PUT", base+"/"+url.PathEscape(args[2])+"/size", map[string]int{"size_gib": n}, &res); err != nil {
			return err
		}
	case "remove":
		if err := need(args, 3, "vm disk remove VM TARGET [--delete-data --yes]"); err != nil {
			return err
		}
		q := ""
		if hasFlag(args, "--delete-data") {
			if !hasFlag(args, "--yes") {
				return errors.New(c.msg("deleting disk data needs --yes", "Löschen der Daten erfordert --yes"))
			}
			q = "?delete_volume=true&confirm=" + url.QueryEscape(args[2])
		}
		if err := c.do("DELETE", base+"/"+url.PathEscape(args[2])+q, nil, &res); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown disk command %q", args[0])
	}
	c.printChange(res)
	return nil
}

func (c *cli) vmNIC(args []string) error {
	if err := need(args, 3, "vm nic add VM network:<name>|bridge:<name> | vm nic remove VM MAC"); err != nil {
		return err
	}
	base := "/vms/" + url.PathEscape(args[1]) + "/nics"
	var res map[string]any
	switch args[0] {
	case "add":
		if err := c.do("POST", base, map[string]string{"network": args[2], "model": "virtio"}, &res); err != nil {
			return err
		}
	case "remove":
		if err := c.do("DELETE", base+"/"+url.PathEscape(args[2]), nil, &res); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown nic command %q", args[0])
	}
	c.printChange(res)
	return nil
}

func (c *cli) vmCDROM(args []string) error {
	if err := need(args, 3, "vm cdrom insert VM TARGET POOL/FILE | vm cdrom eject VM TARGET"); err != nil {
		return err
	}
	path := "/vms/" + url.PathEscape(args[1]) + "/cdrom/" + url.PathEscape(args[2])
	body := map[string]string{}
	if args[0] == "insert" {
		if err := need(args, 4, "vm cdrom insert VM TARGET POOL/FILE"); err != nil {
			return err
		}
		p, f, ok := strings.Cut(args[3], "/")
		if !ok {
			return errors.New("expected POOL/FILE")
		}
		body["pool"], body["volume"] = p, f
	} else if args[0] != "eject" {
		return fmt.Errorf("unknown cdrom command %q", args[0])
	}
	var res map[string]any
	if err := c.do("PUT", path, body, &res); err != nil {
		return err
	}
	c.printChange(res)
	return nil
}
