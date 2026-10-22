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
func (g *Gateway) ProbeProxies(parent context.Context, timeout time.Duration) []ProxyProbeResult {
	results := make([]ProxyProbeResult, len(g.transports.items))
	semaphore := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for i, proxy := range g.transports.items {
		wg.Add(1)
		go func(i int, proxy *proxyTransport) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-parent.Done():
				results[i] = ProxyProbeResult{Index: i, Address: config.RedactURL(proxy.name), Error: "probe canceled", Timeout: parent.Err() == context.DeadlineExceeded}
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
			results[i] = result
		}(i, proxy)
	}
	wg.Wait()
	return results
}
