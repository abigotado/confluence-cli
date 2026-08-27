package confluence

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverCloudIDUsesPublicUnauthenticatedTenantInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != tenantInfoPath || request.Header.Get("Authorization") != "" || request.Header.Get("Accept-Encoding") != "gzip" {
			t.Errorf("discovery request path=%s hasAuth=%v", request.URL.Path, request.Header.Get("Authorization") != "")
		}
		_, _ = io.WriteString(writer, `{"cloudId":"cloud-id"}`)
	}))
	defer server.Close()
	client := routedClient(t, server, func(request *http.Request) {
		if request.URL.String() != "https://tenant.atlassian.net"+tenantInfoPath {
			t.Errorf("discovery URL = %s", request.URL.String())
		}
	})
	cloudID, err := DiscoverCloudID(context.Background(), "https://tenant.atlassian.net", client)
	if err != nil || cloudID != "cloud-id" {
		t.Fatalf("cloudID=%q err=%v", cloudID, err)
	}
}

func TestDiscoverCloudIDBoundsDecompressedResponse(t *testing.T) {
	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	_, _ = zipper.Write(bytes.Repeat([]byte("x"), maxDecompressedBody+1))
	_ = zipper.Close()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Encoding", "gzip")
		_, _ = writer.Write(compressed.Bytes())
	}))
	defer server.Close()
	client := routedClient(t, server, nil)
	_, err := DiscoverCloudID(context.Background(), "https://tenant.atlassian.net", client)
	if err == nil || strings.Contains(err.Error(), strings.Repeat("x", 16)) {
		t.Fatalf("err=%v", err)
	}
}

func TestDiscoverCloudIDRejectsInvalidSitesBeforeNetwork(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	})}
	_, err := DiscoverCloudID(context.Background(), "https://tenant.atlassian.net.evil.example", client)
	if err == nil || calls != 0 || strings.Contains(err.Error(), "evil.example") {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
