// Package unlock wraps MediaUnlockTest to run streaming-region checks
// and return structured results.
package unlock

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	core "MediaUnlockTest/pkg/core"
	providers "MediaUnlockTest/pkg/providers"
	"github.com/xiangaodev/next-looking-glass/internal/i18n"
)

// Status is a summary of a single service check.
type Status struct {
	Name   string `json:"name"`   // e.g. "Netflix"
	Result string `json:"result"` // "YES" | "NO" | "Banned" | "ERR" | "Restricted" | "Failed"
	Region string `json:"region"` // e.g. "HK"
	Info   string `json:"info"`   // additional note
}

// ByCategory groups service results.
type ByCategory struct {
	Category string   `json:"category"`
	Services []Status `json:"services"`
}

// Result is the full output of a MediaUnlockTest run.
type Result struct {
	IPv4       string       `json:"ipv4"`
	ISP        string       `json:"isp"`
	Categories []ByCategory `json:"categories"`
}

// Categories maps readable names to test lists.
type category struct {
	name  string
	tests []providers.TestItem
}

var allCategories = []category{
	{"cat_global", providers.GlobeTests},
	{"cat_taiwan", providers.TaiwanTests},
	{"cat_hongkong", providers.HongKongTests},
	{"cat_japan", providers.JapanTests},
	{"cat_korea", providers.KoreaTests},
	{"cat_na", providers.NorthAmericaTests},
	{"cat_sa", providers.SouthAmericaTests},
	{"cat_eu", providers.EuropeTests},
	{"cat_africa", providers.AfricaTests},
	{"cat_sea", providers.SouthEastAsiaTests},
	{"cat_oceania", providers.OceaniaTests},
	{"cat_ai", providers.AITests},
}

// Run executes all region-unlock checks and returns the aggregated result.
// lang is used to translate category names.
func Run(ctx context.Context, lang i18n.Lang) (*Result, error) {
	core.InitClients()

	// Fetch the node's public IPv4 info for display.
	var ipv4, isp string
	if info, err := core.GetDetailedIPInfo("https://unlock.icmp.ing/api/ip-info", 4); err == nil {
		ipv4 = info.IP
		isp = fmt.Sprintf("%s (AS%d)", info.Organization, info.ASN)
	}

	var wg sync.WaitGroup
	cats := make([]ByCategory, len(allCategories))
	sem := make(chan struct{}, 30) // max 30 concurrent HTTP calls, matching CLI default

	for i, c := range allCategories {
		wg.Add(1)
		go func(idx int, cat category) {
			defer wg.Done()
			var services []Status
			var inner sync.WaitGroup
			var mu sync.Mutex
			for _, t := range cat.tests {
				if t.Func == nil {
					continue
				}
				inner.Add(1)
				sem <- struct{}{}
				go func(tt providers.TestItem) {
				defer func() {
					<-sem
					if r := recover(); r != nil {
						log.Printf("panic in unlock test %q: %v", tt.Name, r)
					}
					inner.Done()
				}()
				select {
					case <-ctx.Done():
						return
					default:
					}
					r := tt.Func(core.AutoHttpClient)
					mu.Lock()
					services = append(services, Status{
						Name:   tt.Name,
						Result: statusLabel(r),
						Region: r.Region,
						Info:   r.Info,
					})
					mu.Unlock()
				}(t)
			}
			inner.Wait()
			sort.Slice(services, func(a, b int) bool {
				return services[a].Name < services[b].Name
			})
			cats[idx] = ByCategory{Category: i18n.T(lang, cat.name), Services: services}
		}(i, c)
	}
	wg.Wait()

	return &Result{
		IPv4:       ipv4,
		ISP:        isp,
		Categories: cats,
	}, nil
}

func statusLabel(r core.Result) string {
	switch r.Status {
	case core.StatusOK:
		return "YES"
	case core.StatusNo:
		return "NO"
	case core.StatusBanned:
		return "Banned"
	case core.StatusNetworkErr:
		return "ERR"
	case core.StatusErr:
		return "ERR"
	case core.StatusRestricted:
		return "Restricted"
	case core.StatusFailed:
		return "Failed"
	case core.StatusUnexpected:
		return "Unexpected"
	default:
		return "Unknown"
	}
}
