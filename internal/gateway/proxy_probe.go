package gateway

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/identity"
	"opencode2api/internal/models"
	wire "opencode2api/internal/protocol"
)

// ProbeProxies makes one real upstream models request through each selected
// proxy. This intentionally observes upstream 403/429 responses and can use
// the upstream IP quota, just like a normal gateway request.
func (g *Gateway) ProbeProxies(ctx context.Context, timeout time.Duration, indexes []int, channel string) []ProxyProbeResult {
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
			channels := []string{channel}
			if channel == "auto" {
				channels = []string{"anonymous", "zen", "go"}
			}
			var last probeResult
			for _, candidate := range channels {
				base, key, tier, channelErr := g.probeCredential(index, candidate)
				if channelErr != nil {
					last = probeResult{reason: channelErr.Error()}
					continue
				}
				model, client, protocol, modelErr := g.probeModel(proxy, base, tier, candidate == "anonymous")
				if modelErr != nil {
					last = probeResult{reason: modelErr.Error()}
					continue
				}
				result.Model = model
				result.Channel = candidate
				last = g.probeAttempt(probeCtx, client, base, key, model, protocol, identity.RequestIDs{Project: "opencode2api:proxy-health"}, "")
				if last.success {
					break
				}
			}
			result.Status = last.status
			result.Duration = time.Since(started).Milliseconds()
			if !last.success {
				result.Error = last.reason
				result.Timeout = last.reason == "probe_timeout"
			}
			results[resultIndex] = result
		}(resultIndex, index, proxy)
	}
	wg.Wait()
	return results
}

func (g *Gateway) probeCredential(proxyIndex int, channel string) (string, string, config.Tier, error) {
	switch channel {
	case "anonymous":
		if !g.cfg.Anonymous {
			return "", "", "", errors.New("anonymous channel is disabled")
		}
		return g.cfg.Upstream.Anonymous, anonymousZenKey, config.TierZen, nil
	case "zen":
		if len(g.cfg.ZenKeys) == 0 {
			return "", "", "", errors.New("no Zen keys are configured")
		}
		return g.cfg.Upstream.Zen, g.cfg.ZenKeys[proxyIndex%len(g.cfg.ZenKeys)], config.TierZen, nil
	case "go":
		if len(g.cfg.GoKeys) == 0 {
			return "", "", "", errors.New("no Go keys are configured")
		}
		return g.cfg.Upstream.Go, g.cfg.GoKeys[proxyIndex%len(g.cfg.GoKeys)], config.TierGo, nil
	default:
		return "", "", "", errors.New("unknown probe channel")
	}
}

func (g *Gateway) probeModel(proxy *proxyTransport, base string, tier config.Tier, anonymous bool) (string, *http.Client, wire.Protocol, error) {
	for _, model := range g.catalog.List() {
		if !g.catalog.IsFreeModel(model) {
			continue
		}
		if _, err := g.catalog.RouteForTier(model, tier, tier == config.TierZen && len(g.cfg.ZenKeys) > 0, tier == config.TierGo && len(g.cfg.GoKeys) > 0); err != nil {
			continue
		}
		var route models.Route
		var err error
		if anonymous {
			route, err = g.catalog.Route(model, len(g.cfg.ZenKeys) > 0, len(g.cfg.GoKeys) > 0, true)
		} else {
			route, err = g.catalog.RouteForTier(model, tier, tier == config.TierZen && len(g.cfg.ZenKeys) > 0, tier == config.TierGo && len(g.cfg.GoKeys) > 0)
		}
		if err != nil {
			continue
		}
		protocol := route.ProtocolFor(tier)
		return model, g.inferenceClient(proxy, base, model), protocol, nil
	}
	return "", nil, "", errors.New("no free model is available on selected channel")
}
