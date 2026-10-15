package lxc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Image is one entry of the LXC image server index.
type Image struct {
	Distribution string `json:"distribution"`
	Release      string `json:"release"`
	Arch         string `json:"arch"`
	Build        string `json:"build"`
}

const imageIndexURL = "https://images.linuxcontainers.org/meta/1.0/index-system"

var (
	imgMu      sync.Mutex
	imgCache   []Image
	imgFetched time.Time
)

// ListImages returns the default-variant images of the official image server
// for the given architecture. The index is cached for an hour.
func ListImages(ctx context.Context, arch string) ([]Image, error) {
	imgMu.Lock()
	defer imgMu.Unlock()
	if imgCache == nil || time.Since(imgFetched) > time.Hour {
		all, err := fetchIndex(ctx)
		if err != nil {
			if imgCache != nil {
				return filterArch(imgCache, arch), nil
			}
			return nil, err
		}
		imgCache, imgFetched = all, time.Now()
	}
	return filterArch(imgCache, arch), nil
}

func filterArch(all []Image, arch string) []Image {
	out := []Image{}
	for _, i := range all {
		if i.Arch == arch {
			out = append(out, i)
		}
	}
	return out
}

func fetchIndex(ctx context.Context) ([]Image, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageIndexURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch image index: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch image index: HTTP %d", resp.StatusCode)
	}
	return parseIndex(io.LimitReader(resp.Body, 4<<20)), nil
}

func parseIndex(r io.Reader) []Image {
	var out []Image
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Split(strings.TrimSpace(sc.Text()), ";")
		if len(f) < 5 || f[3] != "default" {
			continue
		}
		out = append(out, Image{Distribution: f[0], Release: f[1], Arch: f[2], Build: f[4]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Distribution != out[j].Distribution {
			return out[i].Distribution < out[j].Distribution
		}
		return out[i].Release < out[j].Release
	})
	return out
}
