package executor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type proxyAwareHTTPClientCacheEntry struct {
	client   *http.Client
	lastUsed time.Time
}

var (
	proxyAwareHTTPClientCache            = make(map[string]proxyAwareHTTPClientCacheEntry)
	proxyAwareHTTPClientCacheMu          sync.RWMutex
	proxyAwareHTTPClientCacheSingleGroup singleflight.Group
	proxyAwareHTTPClientCacheCleanupOnce sync.Once
)

const (
	proxyAwareHTTPClientCacheTTL             = 30 * time.Minute
	proxyAwareHTTPClientCacheCleanupInterval = 10 * time.Minute
	proxyAwareHTTPClientCacheMaxEntries      = 512
	proxyAwareHTTPClientCacheProfileVersion  = "v1"
)

func getOrCreateProxyAwareHTTPClient(cacheKey string, builder func() *http.Client) *http.Client {
	if builder == nil {
		return nil
	}
	if cacheKey == "" {
		return builder()
	}
	proxyAwareHTTPClientCacheCleanupOnce.Do(startProxyAwareHTTPClientCacheCleanup)

	if client := loadProxyAwareHTTPClient(cacheKey); client != nil {
		return client
	}

	value, _, _ := proxyAwareHTTPClientCacheSingleGroup.Do(cacheKey, func() (any, error) {
		if client := loadProxyAwareHTTPClient(cacheKey); client != nil {
			return client, nil
		}
		client := builder()
		storeProxyAwareHTTPClient(cacheKey, client)
		return client, nil
	})
	if client, ok := value.(*http.Client); ok && client != nil {
		return client
	}
	return builder()
}

func loadProxyAwareHTTPClient(cacheKey string) *http.Client {
	now := time.Now()

	proxyAwareHTTPClientCacheMu.RLock()
	entry, ok := proxyAwareHTTPClientCache[cacheKey]
	proxyAwareHTTPClientCacheMu.RUnlock()
	if !ok || entry.client == nil {
		return nil
	}

	if now.Sub(entry.lastUsed) > proxyAwareHTTPClientCacheTTL {
		evictProxyAwareHTTPClient(cacheKey, entry.client)
		return nil
	}

	proxyAwareHTTPClientCacheMu.Lock()
	if current, exists := proxyAwareHTTPClientCache[cacheKey]; exists && current.client == entry.client {
		current.lastUsed = now
		proxyAwareHTTPClientCache[cacheKey] = current
	}
	proxyAwareHTTPClientCacheMu.Unlock()

	return entry.client
}

func storeProxyAwareHTTPClient(cacheKey string, client *http.Client) {
	if cacheKey == "" || client == nil {
		return
	}

	now := time.Now()
	proxyAwareHTTPClientCacheMu.Lock()
	proxyAwareHTTPClientCache[cacheKey] = proxyAwareHTTPClientCacheEntry{
		client:   client,
		lastUsed: now,
	}
	for len(proxyAwareHTTPClientCache) > proxyAwareHTTPClientCacheMaxEntries {
		evictOldestProxyAwareHTTPClientLocked()
	}
	proxyAwareHTTPClientCacheMu.Unlock()
}

func evictProxyAwareHTTPClient(cacheKey string, expected *http.Client) {
	if cacheKey == "" || expected == nil {
		return
	}
	var victim *http.Client
	proxyAwareHTTPClientCacheMu.Lock()
	if current, exists := proxyAwareHTTPClientCache[cacheKey]; exists && current.client == expected {
		victim = current.client
		delete(proxyAwareHTTPClientCache, cacheKey)
	}
	proxyAwareHTTPClientCacheMu.Unlock()
	closeIdleHTTPClient(victim)
}

func evictOldestProxyAwareHTTPClientLocked() {
	var (
		oldestKey  string
		oldestTime time.Time
		oldest     *http.Client
	)
	for key, entry := range proxyAwareHTTPClientCache {
		if entry.client == nil {
			oldestKey = key
			oldest = nil
			break
		}
		if oldestKey == "" || entry.lastUsed.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.lastUsed
			oldest = entry.client
		}
	}
	if oldestKey == "" {
		return
	}
	delete(proxyAwareHTTPClientCache, oldestKey)
	// Close idle connections outside lock via defer-like pattern.
	go closeIdleHTTPClient(oldest)
}

func startProxyAwareHTTPClientCacheCleanup() {
	go func() {
		ticker := time.NewTicker(proxyAwareHTTPClientCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredProxyAwareHTTPClients()
		}
	}()
}

func purgeExpiredProxyAwareHTTPClients() {
	now := time.Now()
	var victims []*http.Client

	proxyAwareHTTPClientCacheMu.Lock()
	for key, entry := range proxyAwareHTTPClientCache {
		if entry.client == nil || now.Sub(entry.lastUsed) > proxyAwareHTTPClientCacheTTL {
			if entry.client != nil {
				victims = append(victims, entry.client)
			}
			delete(proxyAwareHTTPClientCache, key)
		}
	}
	proxyAwareHTTPClientCacheMu.Unlock()

	for _, victim := range victims {
		closeIdleHTTPClient(victim)
	}
}

func closeIdleHTTPClient(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if closer, ok := client.Transport.(interface{ CloseIdleConnections() }); ok && closer != nil {
		closer.CloseIdleConnections()
	}
}

func buildProxyAwareHTTPClientCacheKey(timeout time.Duration, proxyURL string, fallbackRT http.RoundTripper) (string, bool) {
	timeoutKey := strconv.FormatInt(timeout.Nanoseconds(), 10)
	proxyURL = strings.TrimSpace(proxyURL)

	if proxyURL != "" {
		fingerprint, ok := proxyURLFingerprint(proxyURL)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("mode=proxy|proxy=%s|timeout=%s|profile=%s",
			fingerprint, timeoutKey, proxyAwareHTTPClientCacheProfileVersion), true
	}

	if fallbackRT != nil {
		rtToken, ok := roundTripperCacheToken(fallbackRT)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("mode=fallback_rt|rt=%s|timeout=%s|profile=%s",
			rtToken, timeoutKey, proxyAwareHTTPClientCacheProfileVersion), true
	}

	return fmt.Sprintf("mode=direct|timeout=%s|profile=%s",
		timeoutKey, proxyAwareHTTPClientCacheProfileVersion), true
}

func proxyURLFingerprint(proxyURL string) (string, bool) {
	parsedURL, errParse := url.Parse(strings.TrimSpace(proxyURL))
	if errParse != nil {
		return "", false
	}

	scheme := strings.ToLower(strings.TrimSpace(parsedURL.Scheme))
	switch scheme {
	case "socks5", "http", "https":
	default:
		return "", false
	}

	host := strings.ToLower(strings.TrimSpace(parsedURL.Hostname()))
	if host == "" {
		return "", false
	}

	port := strings.TrimSpace(parsedURL.Port())
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		case "socks5":
			port = "1080"
		}
	}

	username := "-"
	passwordHash := "-"
	if parsedURL.User != nil {
		if user := strings.TrimSpace(parsedURL.User.Username()); user != "" {
			username = user
		}
		if password, hasPassword := parsedURL.User.Password(); hasPassword {
			passwordHash = sha256Hex(password)
		}
	}

	return fmt.Sprintf("%s://%s@%s:%s#%s", scheme, username, host, port, passwordHash), true
}

func roundTripperCacheToken(rt http.RoundTripper) (string, bool) {
	if rt == nil {
		return "", false
	}
	value := reflect.ValueOf(rt)
	switch value.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return fmt.Sprintf("%T:%x", rt, value.Pointer()), true
	default:
		// Value-type RoundTripper instances don't expose a stable identity; skip caching.
		return "", false
	}
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
