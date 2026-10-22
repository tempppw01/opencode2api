package gateway

import (
	"context"
	"sync"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/models"
)

// ProbeProxies checks every route independently using the regular HTTP client.
// It checks the free model catalog endpoint, reports its HTTP status even
// when it is 403/429, and does not change automatic proxy health.
func (g *Gateway) ProbeProxies(parent context.Context, timeout time.Duration, indexes []int, onResult func(ProxyProbeResult)) []ProxyProbeResult {
	if len(indexes) == 0 {
		indexes = make([]int, len(g.transports.items))
		for i := range indexes {
			indexes[i] = i
		}
	}
	results := make([]ProxyProbeResult, len(indexes))
	semaphore := make(chan struct{}, 16)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	storeResult := func(resultIndex int, result ProxyProbeResult) {
		resultMu.Lock()
		results[resultIndex] = result
		resultMu.Unlock()
		if onResult != nil {
			onResult(result)
		}
	}
	for resultIndex, i := range indexes {
		if i < 0 || i >= len(g.transports.items) {
			results[resultIndex] = ProxyProbeResult{Index: i, Error: "invalid proxy index"}
			continue
		}
		proxy := g.transports.items[i]
		wg.Add(1)
		go func(resultIndex, i int, proxy *proxyTransport) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-parent.Done():
				storeResult(resultIndex, ProxyProbeResult{Index: i, Address: config.RedactURL(proxy.name), Error: "probe canceled", Timeout: parent.Err() == context.DeadlineExceeded})
				return
			}
			result := ProxyProbeResult{Index: i, Address: config.RedactURL(proxy.name)}
			started := time.Now()
			ctx, cancel := context.WithTimeout(parent, timeout)
			defer cancel()
			base := g.cfg.Upstream.Anonymous
			if base == "" {
				base = models.V1Base
			}
			_, status, err := models.FetchModels(ctx, proxy.client, base, anonymousZenKey)
			result.Status = status
			result.Duration = time.Since(started).Milliseconds()
			if err != nil {
				result.Error = "proxy request failed"
				result.Timeout = ctx.Err() == context.DeadlineExceeded
			}
			storeResult(resultIndex, result)
		}(resultIndex, i, proxy)
	}
	wg.Wait()
	return results
}
