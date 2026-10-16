// Command hostmanaged runs the control plane ("server"), the privileged node
// agent ("agent") and administrative maintenance tasks.
package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vaktikos/host-managed/internal/agent"
	"github.com/vaktikos/host-managed/internal/agentclient"
	"github.com/vaktikos/host-managed/internal/api"
	"github.com/vaktikos/host-managed/internal/auth"
	"github.com/vaktikos/host-managed/internal/config"
	"github.com/vaktikos/host-managed/internal/hostinfo"
	"github.com/vaktikos/host-managed/internal/jobs"
	"github.com/vaktikos/host-managed/internal/lxc"
	"github.com/vaktikos/host-managed/internal/secrets"
	"github.com/vaktikos/host-managed/internal/store"
	"github.com/vaktikos/host-managed/internal/virt"
	"github.com/vaktikos/host-managed/internal/webui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const defaultConfig = "/etc/hostmanaged/hostmanaged.conf"

func usage() {
	fmt.Fprint(os.Stderr, `Usage: hostmanaged <command> [options]

Commands / Befehle:
  server                     run the control plane (API and web UI)
  agent                      run the privileged node agent (root)
  admin create               create an administrator / Administrator anlegen
  admin reset-password       set a new password for a user
  admin reset-mfa            remove a user's two-factor authentication
  backup                     back up control plane data / Verwaltungsdaten sichern
  version                    print version

Global option: -config <path> (default /etc/hostmanaged/hostmanaged.conf)
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = runServer(os.Args[2:])
	case "agent":
		err = runAgent(os.Args[2:])
	case "admin":
		err = runAdmin(os.Args[2:])
	case "backup":
		err = runBackup(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func logger() *slog.Logger {
	lvl := slog.LevelInfo
	if os.Getenv("HM_DEBUG") == "1" {
		lvl = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

func loadConfig(fsFlags *flag.FlagSet, args []string) (config.Config, error) {
	path := fsFlags.String("config", defaultConfig, "configuration file")
	if err := fsFlags.Parse(args); err != nil {
		return config.Config{}, err
	}
	return config.Load(*path)
}

func runServer(args []string) error {
	cfg, err := loadConfig(flag.NewFlagSet("server", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	log := logger()
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "hostmanaged.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jr := jobs.New(st, log, cfg.JobParallelism)
	jr.Recover(ctx)
	ag := agentclient.New(cfg.AgentSocket)

	var web fs.FS
	if cfg.WebDir != "" {
		web = os.DirFS(cfg.WebDir)
	} else {
		web = webui.FS()
	}
	if web == nil {
		log.Warn("web UI not included in this build; only the API is served")
	}
	box, err := secrets.LoadOrCreate(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		return err
	}
	s := api.New(cfg, st, ag, jr, log, version, web, box)

	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = st.PruneSessions(context.Background())
				_ = st.PruneJobs(context.Background(), 90*24*time.Hour)
			}
		}
	}()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() {
		if cfg.InsecureHTTP {
			log.Warn("serving plain HTTP (insecure_http = true); use only for development", "listen", cfg.Listen)
			errc <- srv.ListenAndServe()
		} else {
			log.Info("serving HTTPS", "listen", cfg.Listen, "version", version)
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		}
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		jr.Shutdown(10 * time.Second)
	}
	return nil
}

func runAgent(args []string) error {
	cfg, err := loadConfig(flag.NewFlagSet("agent", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("the agent must run as root")
	}
	log := logger()
	gid := -1
	var allowed []uint32
	if g, err := user.LookupGroup(cfg.AgentGroup); err == nil {
		gid, _ = strconv.Atoi(g.Gid)
	} else {
		log.Warn("agent group not found; socket stays root-only", "group", cfg.AgentGroup)
	}
	if u, err := user.Lookup(cfg.AllowedUser); err == nil {
		uid, _ := strconv.ParseUint(u.Uid, 10, 32)
		allowed = append(allowed, uint32(uid))
	}
	if err := os.MkdirAll(filepath.Dir(cfg.AgentSocket), 0o750); err != nil {
		return err
	}
	if gid >= 0 {
		_ = os.Chown(filepath.Dir(cfg.AgentSocket), 0, gid)
	}
	ln, err := agent.Listen(cfg.AgentSocket, gid)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.AgentSocket, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	vm := virt.New(virt.Options{AllowTCG: cfg.AllowSoftwareEmulation})
	defer vm.Close()
	vm.StartCollector(5 * time.Second)
	cpu := &hostinfo.CPUSampler{}
	go cpu.Run(5*time.Second, ctx.Done())

	srv := &agent.Server{
		Virt: vm, LXC: lxc.New(), CPU: cpu, Version: version, Log: log,
		AllowedUIDs: allowed, MaxUpload: cfg.MaxUploadGiB << 30,
		AllowPrivateDownloads: cfg.AllowPrivateDownloads,
	}
	log.Info("agent listening", "socket", cfg.AgentSocket, "version", version, "kvm", virt.KVMAvailable())
	return srv.Serve(ctx, ln)
}

func openStoreForAdmin(cfg config.Config) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	return store.Open(filepath.Join(cfg.DataDir, "hostmanaged.db"))
}

func readPassword(fromStdin bool) (pw string, generated bool, err error) {
	if !fromStdin {
		return auth.RandomPassword(), true, nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", false, err
	}
	return strings.TrimRight(line, "\r\n"), false, nil
}

func runAdmin(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: hostmanaged admin create|reset-password --username NAME [--password-stdin]")
	}
	sub := args[0]
	fl := flag.NewFlagSet("admin "+sub, flag.ExitOnError)
	name := fl.String("username", "admin", "user name")
	stdin := fl.Bool("password-stdin", false, "read the password from standard input instead of generating one")
	ifMissing := fl.Bool("if-no-users", false, "create only if the database has no users yet (installer)")
	lang := fl.String("language", "de", "UI language (de or en)")
	cfg, err := loadConfig(fl, args[1:])
	if err != nil {
		return err
	}
	st, err := openStoreForAdmin(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	switch sub {
	case "create":
		if *ifMissing {
			if n, _ := st.CountUsers(ctx); n > 0 {
				fmt.Println("users already exist; nothing to do")
				return nil
			}
		}
		pw, gen, err := readPassword(*stdin)
		if err != nil {
			return err
		}
		if err := auth.CheckPasswordPolicy(pw); err != nil {
			return err
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		if _, err := st.CreateUser(ctx, *name, hash, "admin", *lang); err != nil {
			if errors.Is(err, store.ErrExists) {
				return fmt.Errorf("user %q already exists (use admin reset-password)", *name)
			}
			return err
		}
		_ = st.Audit(ctx, store.AuditEntry{Username: "cli", Action: "user.create", Resource: "user/" + *name, Result: "ok", Detail: "role=admin"})
		fmt.Printf("Administrator %q created.\n", *name)
		if gen {
			fmt.Printf("Password / Passwort: %s\n", pw)
			fmt.Println("Store it safely and change it after the first login. / Sicher aufbewahren und nach der ersten Anmeldung ändern.")
		}
	case "reset-password":
		u, err := st.UserByName(ctx, *name)
		if err != nil {
			return fmt.Errorf("user %q not found", *name)
		}
		pw, gen, err := readPassword(*stdin)
		if err != nil {
			return err
		}
		if err := auth.CheckPasswordPolicy(pw); err != nil {
			return err
		}
		hash, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		if err := st.SetPassword(ctx, u.ID, hash); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(ctx, u.ID, "")
		_ = st.SetDisabled(ctx, u.ID, false)
		_ = st.Audit(ctx, store.AuditEntry{Username: "cli", Action: "user.password.reset", Resource: "user/" + *name, Result: "ok"})
		fmt.Printf("Password for %q reset; all sessions were signed out.\n", *name)
		if gen {
			fmt.Printf("Password / Passwort: %s\n", pw)
		}
	case "reset-mfa":
		u, err := st.UserByName(ctx, *name)
		if err != nil {
			return fmt.Errorf("user %q not found", *name)
		}
		if err := st.DisableTOTP(ctx, u.ID); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(ctx, u.ID, "")
		_ = st.Audit(ctx, store.AuditEntry{Username: "cli", Action: "user.mfa.reset", Resource: "user/" + *name, Result: "ok"})
		fmt.Printf("Two-factor authentication for %q removed; all sessions were signed out.\n", *name)
	default:
		return fmt.Errorf("unknown admin command %q", sub)
	}
	return nil
}

// runBackup writes a tar.gz with a consistent database snapshot and the
// configuration directory (including the TLS key). The archive is created
// with mode 0600 because it contains secrets.
func runBackup(args []string) error {
	fl := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fl.String("output", "", "archive path (default: <data_dir>/backups/hostmanaged-<time>.tar.gz)")
	confDir := fl.String("config-dir", "/etc/hostmanaged", "configuration directory to include")
	cfg, err := loadConfig(fl, args)
	if err != nil {
		return err
	}
	if *out == "" {
		dir := filepath.Join(cfg.DataDir, "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		*out = filepath.Join(dir, "hostmanaged-"+time.Now().UTC().Format("20060102-150405")+".tar.gz")
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "hostmanaged.db"))
	if err != nil {
		return err
	}
	snap := filepath.Join(os.TempDir(), "hm-backup-"+auth.RandomToken(6)+".db")
	err = st.Backup(context.Background(), snap)
	st.Close()
	if err != nil {
		return fmt.Errorf("database snapshot: %w", err)
	}
	defer os.Remove(snap)

	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	add := func(src, name string) error {
		fi, err := os.Stat(src)
		if err != nil {
			return err
		}
		hdr, _ := tar.FileInfoHeader(fi, "")
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	}
	err = add(snap, "data/hostmanaged.db")
	if err == nil {
		if _, serr := os.Stat(filepath.Join(cfg.DataDir, "secret.key")); serr == nil {
			err = add(filepath.Join(cfg.DataDir, "secret.key"), "data/secret.key")
		}
	}
	if err == nil {
		err = filepath.WalkDir(*confDir, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				if os.IsNotExist(werr) {
					return nil
				}
				return werr
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(*confDir, p)
			return add(p, "config/"+rel)
		})
	}
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(*out)
		return err
	}
	fmt.Println(*out)
	return nil
}
