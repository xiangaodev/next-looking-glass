// Package unlock wraps MediaUnlockTest to run streaming-region checks
// and return structured results.  Execution model mirrors the CLI's
// ExecuteTestsParallel: flat worker pool with default 30 concurrency.
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

type Status struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Region string `json:"region"`
	Info   string `json:"info"`
}

type ByCategory struct {
	Category string   `json:"category"`
	Services []Status `json:"services"`
}

type Result struct {
	IPv4       string       `json:"ipv4"`
	ISP        string       `json:"isp"`
	Categories []ByCategory `json:"categories"`
}

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

type testJob struct {
	test   providers.TestItem
	region string // category key for i18n
}

// Run executes all region-unlock checks.  Mirrors the CLI's
// ExecuteTestsParallel worker-pool pattern exactly.
func Run(ctx context.Context, lang i18n.Lang) (*Result, error) {
	// Fetch public IP info.
	var ipv4, isp string
	if info, err := core.GetDetailedIPInfo("https://unlock.icmp.ing/api/ip-info", 4); err == nil {
		ipv4 = info.IP
		isp = fmt.Sprintf("%s (AS%d)", info.Organization, info.ASN)
	}

	// Collect all tests into a flat list, matching CLI's approach.
	var jobs []testJob
	for _, cat := range allCategories {
		for _, t := range cat.tests {
			if t.Func == nil {
				continue
			}
			jobs = append(jobs, testJob{test: t, region: cat.name})
		}
	}
	if len(jobs) == 0 {
		return &Result{IPv4: ipv4, ISP: isp}, nil
	}

	// Worker pool: same concurrency logic as CLI.
	maxWorkers := 30
	if len(jobs) > 50 {
		maxWorkers = 40
	} else if len(jobs) < 20 {
		maxWorkers = 25
	}
	sem := make(chan struct{}, maxWorkers)

	type jobResult struct {
		region string
		name   string
		status Status
	}
	resultCh := make(chan jobResult, len(jobs))
	var wg sync.WaitGroup

	for _, job := range jobs {
		sem <- struct{}{}
		wg.Add(1)
		go func(j testJob) {
			defer func() {
				<-sem
				if r := recover(); r != nil {
					log.Printf("panic in unlock test %q: %v", j.test.Name, r)
				}
				wg.Done()
			}()

			done := make(chan core.Result, 1)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						done <- core.Result{Status: core.StatusFailed, Info: fmt.Sprintf("panic: %v", r)}
					}
				}()
				done <- j.test.Func(core.AutoHttpClient)
			}()

			var r core.Result
			select {
			case r = <-done:
			case <-ctx.Done():
				return
			}

			resultCh <- jobResult{
				region: j.region,
				name:   j.test.Name,
				status: Status{
					Name:   j.test.Name,
					Result: statusLabel(r),
					Region: r.Region,
					Info:   r.Info,
				},
			}
		}(job)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Group results by region.
	regionMap := make(map[string][]Status)
	for r := range resultCh {
		regionMap[r.region] = append(regionMap[r.region], r.status)
	}

	// Build ordered categories.
	cats := make([]ByCategory, 0, len(allCategories))
	for _, cat := range allCategories {
		services := regionMap[cat.name]
		sort.Slice(services, func(a, b int) bool { return services[a].Name < services[b].Name })
		if len(services) > 0 {
			cats = append(cats, ByCategory{
				Category: i18n.T(lang, cat.name),
				Services: services,
			})
		}
	}

	return &Result{IPv4: ipv4, ISP: isp, Categories: cats}, nil
}

func statusLabel(r core.Result) string {
	switch r.Status {
	case core.StatusOK:
		return "YES"
	case core.StatusNo:
		return "NO"
	case core.StatusBanned:
		return "Banned"
	case core.StatusRestricted:
		return "Restricted"
	case core.StatusNetworkErr, core.StatusErr:
		return "ERR"
	case core.StatusFailed:
		return "Failed"
	case core.StatusUnexpected:
		return "Unexpected"
	}
	return "Unknown"
}
