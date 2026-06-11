package executor

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

const maxProxyTransportCacheEntries = 128

var sharedProxyTransportCache = newProxyTransportCache()

type proxyTransportCacheKey struct {
	proxyURL           string
	preferIPv4         bool
	insecureSkipVerify bool
	caCert             string
	caCertStat         string
}

type proxyTransportCacheEntry struct {
	transport *http.Transport
	lastUsed  time.Time
}

type proxyTransportCache struct {
	mu         sync.Mutex
	transports map[proxyTransportCacheKey]*proxyTransportCacheEntry
}

func newProxyTransportCache() *proxyTransportCache {
	return &proxyTransportCache{
		transports: make(map[proxyTransportCacheKey]*proxyTransportCacheEntry),
	}
}

func cachedProxyTransport(proxyURL string, sdkCfg *config.SDKConfig) *http.Transport {
	return sharedProxyTransportCache.get(proxyURL, sdkCfg)
}

func (c *proxyTransportCache) get(proxyURL string, sdkCfg *config.SDKConfig) *http.Transport {
	if c == nil {
		return nil
	}
	key := newProxyTransportCacheKey(proxyURL, sdkCfg)
	if key.proxyURL == "" {
		return nil
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry := c.transports[key]; entry != nil {
		entry.lastUsed = now
		return entry.transport
	}

	transport := util.BuildProxyTransport(key.proxyURL, key.preferIPv4)
	if transport == nil {
		return nil
	}
	util.ApplyTLSConfig(transport, sdkCfg)

	if len(c.transports) >= maxProxyTransportCacheEntries {
		c.evictOldestLocked()
	}
	c.transports[key] = &proxyTransportCacheEntry{
		transport: transport,
		lastUsed:  now,
	}
	return transport
}

func (c *proxyTransportCache) evictOldestLocked() {
	var oldestKey proxyTransportCacheKey
	var oldestEntry *proxyTransportCacheEntry
	for key, entry := range c.transports {
		if oldestEntry == nil || entry.lastUsed.Before(oldestEntry.lastUsed) {
			oldestKey = key
			oldestEntry = entry
		}
	}
	if oldestEntry == nil {
		return
	}
	delete(c.transports, oldestKey)
	oldestEntry.transport.CloseIdleConnections()
}

func newProxyTransportCacheKey(proxyURL string, sdkCfg *config.SDKConfig) proxyTransportCacheKey {
	key := proxyTransportCacheKey{
		proxyURL: strings.TrimSpace(proxyURL),
	}
	if sdkCfg == nil {
		return key
	}
	key.preferIPv4 = sdkCfg.PreferIPv4
	key.insecureSkipVerify = sdkCfg.InsecureSkipVerify
	key.caCert = strings.TrimSpace(sdkCfg.CACert)
	key.caCertStat = caCertStatFingerprint(key.caCert)
	return key
}

func caCertStatFingerprint(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}
