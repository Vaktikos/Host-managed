package backup

import (
	"fmt"
	"time"
)

// Keep is a retention policy with the same semantics as Proxmox Backup
// Server's prune: the selectors are applied in order (last, hourly, daily,
// weekly, monthly, yearly); each keeps the newest backup of up to N distinct
// periods that no earlier selector already covered. Zero disables a
// selector. Everything not kept is removed, except protected backups.
type Keep struct {
	Last    int `json:"keep_last"`
	Hourly  int `json:"keep_hourly"`
	Daily   int `json:"keep_daily"`
	Weekly  int `json:"keep_weekly"`
	Monthly int `json:"keep_monthly"`
	Yearly  int `json:"keep_yearly"`
}

func (k Keep) Empty() bool {
	return k.Last+k.Hourly+k.Daily+k.Weekly+k.Monthly+k.Yearly == 0
}

func (k Keep) Validate() error {
	for _, v := range []int{k.Last, k.Hourly, k.Daily, k.Weekly, k.Monthly, k.Yearly} {
		if v < 0 || v > 10000 {
			return fmt.Errorf("retention values must be between 0 and 10000")
		}
	}
	return nil
}

// Prune decides which backups (newest first, one group) to remove. An empty
// policy keeps everything. loc is the time zone used for periods.
func Prune(list []Manifest, k Keep, loc *time.Location) (keep, remove []Manifest) {
	if k.Empty() {
		return list, nil
	}
	marked := map[string]bool{}
	sel := func(n int, key func(time.Time) string) {
		if n <= 0 {
			return
		}
		already := map[string]bool{}
		for _, m := range list {
			if marked[m.ID] {
				already[key(m.Created.In(loc))] = true
			}
		}
		include := map[string]bool{}
		for _, m := range list {
			if marked[m.ID] {
				continue
			}
			kk := key(m.Created.In(loc))
			if already[kk] || include[kk] {
				continue
			}
			if len(include) >= n {
				break
			}
			include[kk] = true
			marked[m.ID] = true
		}
	}
	sel(k.Last, func(t time.Time) string { return t.Format(time.RFC3339Nano) })
	sel(k.Hourly, func(t time.Time) string { return t.Format("2006-01-02T15") })
	sel(k.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	sel(k.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-%02d", y, w) })
	sel(k.Monthly, func(t time.Time) string { return t.Format("2006-01") })
	sel(k.Yearly, func(t time.Time) string { return t.Format("2006") })
	for _, m := range list {
		if marked[m.ID] || m.Protected {
			keep = append(keep, m)
		} else {
			remove = append(remove, m)
		}
	}
	return keep, remove
}
