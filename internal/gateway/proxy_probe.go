package gateway

import (
	"context"
	"sync"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/models"
)

// ProbeProxies makes one real upstream models request through each selected
// proxy. This intentionally observes upstream 403/429 responses and can use
// the upstream IP quota, just like a normal gateway request.
func (g *Gateway) ProbeProxies(ctx context.Context, timeout time.Duration, indexes []int) []ProxyProbeResult {
	if len(indexes) == 0 {
		indexes = make([]int, len(g.transports.items))
		for i := range indexes {
			indexes[i] = i
		}
	}
	results := make([]ProxyProbeResult, len(indexes))
	var wg sync.WaitGroup
	for resultIndex, index := range indexes {
		if index < 0 || index >= len(g.transports.items) {
			results[resultIndex] = ProxyProbeResult{Index: index, Error: "invalid proxy index"}
			continue
		}
		proxy := g.transports.items[index]
		wg.Add(1)
		go func(resultIndex, index int, proxy *proxyTransport) {
			defer wg.Done()
			result := ProxyProbeResult{Index: index, Address: config.RedactURL(proxy.name)}
			started := time.Now()
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			base, key, channel := g.probeCredential(index)
			result.Channel = channel
			_, status, err := models.FetchModels(probeCtx, proxy.client, base, key)
			result.Status = status
			result.Duration = time.Since(started).Milliseconds()
			if err != nil {
				result.Error = "upstream request failed"
				result.Timeout = probeCtx.Err() == context.DeadlineExceeded
			}
			results[resultIndex] = result
		}(resultIndex, index, proxy)
	}
	wg.Wait()
	return results
}

func (g *Gateway) probeCredential(proxyIndex int) (string, string, string) {
	if g.cfg.Prefer == config.TierGo && len(g.cfg.GoKeys) > 0 {
		return g.cfg.Upstream.Go, g.cfg.GoKeys[proxyIndex%len(g.cfg.GoKeys)], "go/key"
	}
	if g.cfg.Prefer != config.TierGo && len(g.cfg.ZenKeys) > 0 {
		return g.cfg.Upstream.Zen, g.cfg.ZenKeys[proxyIndex%len(g.cfg.ZenKeys)], "zen/key"
	}
	if g.cfg.Anonymous {
		return g.cfg.Upstream.Anonymous, anonymousZenKey, "anonymous"
	}
	if len(g.cfg.ZenKeys) > 0 {
		return g.cfg.Upstream.Zen, g.cfg.ZenKeys[proxyIndex%len(g.cfg.ZenKeys)], "zen/key"
	}
	if len(g.cfg.GoKeys) > 0 {
		return g.cfg.Upstream.Go, g.cfg.GoKeys[proxyIndex%len(g.cfg.GoKeys)], "go/key"
	}
	return g.cfg.Upstream.Anonymous, anonymousZenKey, "anonymous"
}
