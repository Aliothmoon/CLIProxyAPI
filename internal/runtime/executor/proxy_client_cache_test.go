package executor

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type testRoundTripper struct{}

func (*testRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, nil
}

func resetProxyAwareHTTPClientCacheForTest() {
	var victims []*http.Client
	proxyAwareHTTPClientCacheMu.Lock()
	for _, entry := range proxyAwareHTTPClientCache {
		if entry.client != nil {
			victims = append(victims, entry.client)
		}
	}
	proxyAwareHTTPClientCache = make(map[string]proxyAwareHTTPClientCacheEntry)
	proxyAwareHTTPClientCacheMu.Unlock()

	for _, victim := range victims {
		closeIdleHTTPClient(victim)
	}
}

func TestNewProxyAwareHTTPClient_ReusesClientForSameAuthProxyAndTimeout(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "auth-1",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}

	clientA := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)

	if clientA != clientB {
		t.Fatalf("expected same client instance for identical auth/proxy/timeout")
	}
	if clientA.Transport == nil {
		t.Fatalf("expected proxied client transport to be configured")
	}
}

func TestNewProxyAwareHTTPClient_SameProxyDifferentAuthSharesClient(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	authA := &cliproxyauth.Auth{ID: "auth-A", Provider: "claude", ProxyURL: "socks5://user:pass@127.0.0.1:1080"}
	authB := &cliproxyauth.Auth{ID: "auth-B", Provider: "claude", ProxyURL: "socks5://user:pass@127.0.0.1:1080"}

	clientA := newProxyAwareHTTPClient(context.Background(), nil, authA, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), nil, authB, 0)

	if clientA != clientB {
		t.Fatalf("expected same client instance for same proxy config regardless of auth ID")
	}
}

func TestNewProxyAwareHTTPClient_DifferentTimeoutUsesDifferentClient(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "auth-1",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}

	clientA := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), nil, auth, 3*time.Second)

	if clientA == clientB {
		t.Fatalf("expected different clients when timeout differs")
	}
	if clientB.Timeout != 3*time.Second {
		t.Fatalf("client timeout = %v, want %v", clientB.Timeout, 3*time.Second)
	}
}

func TestNewProxyAwareHTTPClient_ProxyChangeInvalidatesReuse(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "auth-1",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}
	clientA := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)

	auth.ProxyURL = "socks5://user:pass@127.0.0.1:2080"
	clientB := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)

	if clientA == clientB {
		t.Fatalf("expected different clients after proxy URL changes")
	}
}

func TestNewProxyAwareHTTPClient_ReusesDirectClientForSameAuth(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{ID: "auth-direct", Provider: "claude"}

	clientA := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)

	if clientA != clientB {
		t.Fatalf("expected direct mode client reuse for same auth")
	}
}

func TestNewProxyAwareHTTPClient_ReusesFallbackRoundTripperClient(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{ID: "auth-rt", Provider: "claude"}
	rt := &testRoundTripper{}
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", rt)

	clientA := newProxyAwareHTTPClient(ctx, nil, auth, 0)
	clientB := newProxyAwareHTTPClient(ctx, nil, auth, 0)

	if clientA != clientB {
		t.Fatalf("expected fallback round-tripper client reuse for same auth")
	}
	if clientA.Transport != rt {
		t.Fatalf("expected fallback round-tripper to be used as transport")
	}
}

func TestNewProxyAwareHTTPClient_InvalidProxyURLNotCached(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "auth-invalid-proxy",
		Provider: "claude",
		ProxyURL: "://invalid",
	}

	clientA := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), nil, auth, 0)

	if clientA == clientB {
		t.Fatalf("expected invalid proxy config to bypass cache")
	}
}

func TestBuildProxyAwareHTTPClientCacheKey_DoesNotLeakProxyPassword(t *testing.T) {
	key, ok := buildProxyAwareHTTPClientCacheKey(
		0,
		"socks5://user:super-secret@127.0.0.1:1080",
		nil,
	)
	if !ok {
		t.Fatalf("expected key generation success")
	}
	if strings.Contains(key, "super-secret") {
		t.Fatalf("cache key should not contain raw proxy password")
	}
}

func TestNewProxyAwareHTTPClient_ConcurrentSameKeyReturnsSameClient(t *testing.T) {
	resetProxyAwareHTTPClientCacheForTest()
	t.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "auth-concurrent",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}

	const workers = 32
	results := make(chan *http.Client, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			results <- newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
		}()
	}
	wg.Wait()
	close(results)

	var first *http.Client
	for client := range results {
		if first == nil {
			first = client
			continue
		}
		if client != first {
			t.Fatalf("expected all concurrent calls to return the same cached client instance")
		}
	}
}
