package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func BenchmarkProxyAwareHTTPClient_ProxyBuildBaseline(b *testing.B) {
	resetProxyAwareHTTPClientCacheForTest()
	b.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "bench-baseline",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		client := buildProxyAwareHTTPClient(0, auth.ProxyURL, nil)
		if client == nil || client.Transport == nil {
			b.Fatal("expected proxied client transport")
		}
	}
}

func BenchmarkProxyAwareHTTPClient_ProxyCacheHit(b *testing.B) {
	resetProxyAwareHTTPClientCacheForTest()
	b.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "bench-cache-hit",
		Provider: "claude",
		ProxyURL: "socks5://user:pass@127.0.0.1:1080",
	}
	ctx := context.Background()

	// Pre-warm cache.
	_ = newProxyAwareHTTPClient(ctx, nil, auth, 0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client := newProxyAwareHTTPClient(ctx, nil, auth, 0)
		if client == nil {
			b.Fatal("expected cached client")
		}
	}
}

func BenchmarkProxyAwareHTTPClient_ProxyCacheMiss(b *testing.B) {
	resetProxyAwareHTTPClientCacheForTest()
	b.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		auth := &cliproxyauth.Auth{
			ID:       "bench-cache-miss-" + strconv.Itoa(i),
			Provider: "claude",
			ProxyURL: "socks5://user:pass@127.0.0.1:1080",
		}
		client := newProxyAwareHTTPClient(ctx, nil, auth, 0)
		if client == nil || client.Transport == nil {
			b.Fatal("expected proxied client")
		}
	}
}

func benchmarkProxyRoundTrip(b *testing.B, clientBuilder func(proxyURL string) *http.Client) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	proxyTransport := &http.Transport{}
	proxyClient := &http.Client{Transport: proxyTransport}
	defer proxyTransport.CloseIdleConnections()

	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetURL := ""
		if r.URL != nil && r.URL.IsAbs() {
			targetURL = r.URL.String()
		} else if parsed, errParse := url.Parse(r.RequestURI); errParse == nil && parsed.IsAbs() {
			targetURL = parsed.String()
		}
		if targetURL == "" {
			http.Error(w, "invalid proxy request URL", http.StatusBadRequest)
			return
		}

		req, errReq := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
		if errReq != nil {
			http.Error(w, errReq.Error(), http.StatusBadRequest)
			return
		}
		req.Header = r.Header.Clone()

		resp, errDo := proxyClient.Do(req)
		if errDo != nil {
			http.Error(w, errDo.Error(), http.StatusBadGateway)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()

		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client := clientBuilder(proxyServer.URL)
		req, errReq := http.NewRequest(http.MethodGet, target.URL, nil)
		if errReq != nil {
			b.Fatal(errReq)
		}
		resp, errDo := client.Do(req)
		if errDo != nil {
			b.Fatal(errDo)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkProxyAwareHTTPClient_ProxyRoundTripBaseline(b *testing.B) {
	resetProxyAwareHTTPClientCacheForTest()
	b.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "bench-rt-baseline",
		Provider: "claude",
	}

	benchmarkProxyRoundTrip(b, func(proxyURL string) *http.Client {
		auth.ProxyURL = proxyURL
		client := buildProxyAwareHTTPClient(0, auth.ProxyURL, nil)
		return client
	})
}

func BenchmarkProxyAwareHTTPClient_ProxyRoundTripCacheHit(b *testing.B) {
	resetProxyAwareHTTPClientCacheForTest()
	b.Cleanup(resetProxyAwareHTTPClientCacheForTest)

	auth := &cliproxyauth.Auth{
		ID:       "bench-rt-cache-hit",
		Provider: "claude",
	}

	benchmarkProxyRoundTrip(b, func(proxyURL string) *http.Client {
		auth.ProxyURL = proxyURL
		return newProxyAwareHTTPClient(context.Background(), nil, auth, 0)
	})
}
