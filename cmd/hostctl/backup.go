package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strconv"
)

func (c *cli) backup(args []string) error {
	if err := need(args, 1, "backup list|create|restore|delete|protect|unprotect|prune|schedule"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
		store := fs.String("store", "", "backup store (default: all)")
		vm := fs.String("vm", "", "only this VM")
		ct := fs.String("ct", "", "only this container")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		q := url.Values{}
		if *store != "" {
			q.Set("store", *store)
		}
		if *vm != "" {
			q.Set("kind", "vm")
			q.Set("name", *vm)
		} else if *ct != "" {
			q.Set("kind", "ct")
			q.Set("name", *ct)
		}
		var list []map[string]any
		if err := c.do("GET", "/backups?"+q.Encode(), nil, &list); err != nil {
			return err
		}
		if c.json {
			c.printJSON(list)
			return nil
		}
		var rows [][]string
		for _, b := range list {
			prot := ""
			if b["protected"] == true {
				prot = "yes"
			}
			rows = append(rows, []string{fmt.Sprint(b["store"]), fmt.Sprint(b["id"]), fmt.Sprint(b["mode"]), fmt.Sprint(b["consistency"]),
				human(b["size"].(float64)), prot, str(b["notes"])})
		}
		table([]string{"STORE", "ID", "MODE", "CONSISTENCY", "SIZE", "PROTECTED", "NOTES"}, rows)
		return nil
	case "create":
		if err := need(args, 3, "backup create vm|ct NAME [--store backup] [--notes TEXT] [--mode stop|suspend|live] [--wait]"); err != nil {
			return err
		}
		kind, name := args[1], args[2]
		fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
		store := fs.String("store", "backup", "backup store")
		notes := fs.String("notes", "", "notes")
		mode := fs.String("mode", "", "container mode: stop, suspend or live")
		wait := fs.Bool("wait", false, "wait for the job")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		var path string
		switch kind {
		case "vm":
			path = "/vms/"
		case "ct":
			path = "/containers/"
		default:
			return errors.New("backup create expects vm or ct")
		}
		return c.jobFrom("POST", path+url.PathEscape(name)+"/backup", map[string]string{"store": *store, "notes": *notes, "mode": *mode}, *wait)
	case "restore":
		if err := need(args, 3, "backup restore STORE ID [--name NAME] [--pool POOL] [--start] [--wait]"); err != nil {
			return err
		}
		fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		name := fs.String("name", "", "new name (default: original)")
		pool := fs.String("pool", "local", "VMs: target storage pool")
		start := fs.Bool("start", false, "start after restore")
		wait := fs.Bool("wait", false, "wait for the job")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		return c.jobFrom("POST", "/backups/restore", map[string]any{"store": args[1], "id": args[2], "name": *name, "pool": *pool, "start": *start}, *wait)
	case "delete":
		if err := need(args, 3, "backup delete STORE ID --yes"); err != nil {
			return err
		}
		if !hasFlag(args, "--yes") {
			return errors.New(c.msg("refusing to delete without --yes", "Löschen nur mit --yes"))
		}
		if err := c.do("DELETE", "/backups?"+url.Values{"store": {args[1]}, "id": {args[2]}}.Encode(), nil, nil); err != nil {
			return err
		}
		fmt.Println(c.msg("deleted", "gelöscht"))
		return nil
	case "protect", "unprotect":
		if err := need(args, 3, "backup protect|unprotect STORE ID"); err != nil {
			return err
		}
		if err := c.do("PUT", "/backups/protected", map[string]any{"store": args[1], "id": args[2], "protected": args[0] == "protect"}, nil); err != nil {
			return err
		}
		fmt.Println(c.msg("ok", "erledigt"))
		return nil
	case "prune":
		if err := need(args, 3, "backup prune vm|ct NAME [--store backup] --keep-last N [--keep-daily N ...] [--dry-run]"); err != nil {
			return err
		}
		fs := flag.NewFlagSet("backup prune", flag.ContinueOnError)
		store := fs.String("store", "backup", "backup store")
		keep := map[string]*int{}
		for _, k := range []string{"last", "hourly", "daily", "weekly", "monthly", "yearly"} {
			keep["keep_"+k] = fs.Int("keep-"+k, 0, "")
		}
		dry := fs.Bool("dry-run", false, "only show what would be removed")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		k := map[string]int{}
		for n, v := range keep {
			k[n] = *v
		}
		var res struct {
			Kept    []string `json:"kept"`
			Removed []string `json:"removed"`
		}
		if err := c.do("POST", "/backups/prune", map[string]any{"store": *store, "kind": args[1], "name": args[2], "keep": k, "dry_run": *dry}, &res); err != nil {
			return err
		}
		if c.json {
			c.printJSON(res)
			return nil
		}
		verb := c.msg("removed", "entfernt")
		if *dry {
			verb = c.msg("would remove", "würde entfernen")
		}
		for _, id := range res.Kept {
			fmt.Println(c.msg("keep   ", "behalten ") + id)
		}
		for _, id := range res.Removed {
			fmt.Println(verb + " " + id)
		}
		return nil
	case "schedule":
		return c.backupSchedule(args[1:])
	}
	return fmt.Errorf("unknown backup command %q", args[0])
}

func (c *cli) backupSchedule(args []string) error {
	if err := need(args, 1, "backup schedule list | run ID"); err != nil {
		return err
	}
	switch args[0] {
	case "list":
		var list []map[string]any
		if err := c.do("GET", "/backup-schedules", nil, &list); err != nil {
			return err
		}
		if c.json {
			c.printJSON(list)
			return nil
		}
		var rows [][]string
		for _, s := range list {
			next := "-"
			if v, ok := s["next_run"].(string); ok {
				next = v
			}
			rows = append(rows, []string{strconv.Itoa(int(s["id"].(float64))), fmt.Sprint(s["name"]), fmt.Sprint(s["enabled"]),
				fmt.Sprint(s["store"]), fmt.Sprint(s["at"]), fmt.Sprint(s["selection"]), next})
		}
		table([]string{"ID", "NAME", "ENABLED", "STORE", "AT", "SELECTION", "NEXT"}, rows)
		return nil
	case "run":
		if err := need(args, 2, "backup schedule run ID [--wait]"); err != nil {
			return err
		}
		return c.jobFrom("POST", "/backup-schedules/"+url.PathEscape(args[1])+"/run", nil, hasFlag(args, "--wait"))
	}
	return fmt.Errorf("unknown schedule command %q", args[0])
}

// str prints a JSON value, with absent values as empty strings.
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
