package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/abigotado/confluence-cli/internal/atlassian"
	"github.com/abigotado/confluence-cli/internal/errx"
)

// DiscoverCloudID resolves a validated Atlassian tenant using the public,
// unauthenticated tenant-info endpoint. Redirects are refused.
func DiscoverCloudID(ctx context.Context, site string, httpClient *http.Client) (string, error) {
	if err := atlassian.ValidateSite(site); err != nil {
		return "", errx.Usage("Confluence site must be exactly https://<tenant>.atlassian.net")
	}
	client := httpClient
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.CheckRedirect = refuseRedirect
	clone.Jar = nil

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, site+tenantInfoPath, nil)
	if err != nil {
		return "", errx.Internal("could not build cloud ID discovery request")
	}
	request.Header.Set("Accept", "application/json")
	// Keep automatic decompression disabled so the same independent compressed
	// and decompressed response limits used by authenticated calls apply here.
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := clone.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", errx.Translate(ctx.Err())
		}
		return "", errx.Retryable("NETWORK", 0, "could not reach Atlassian cloud ID discovery")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBody))
		_ = response.Body.Close()
	}()
	body, tooLarge, readErr := readResponseBody(response)
	if readErr != nil {
		return "", errx.Internal("could not read cloud ID discovery response")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return "", errx.Retryable("RATE_LIMITED", parseRetryAfter(response.Header.Get("Retry-After"), time.Now()), "cloud ID discovery was rate limited")
	}
	if response.StatusCode >= http.StatusInternalServerError {
		return "", errx.Retryable("SERVER_ERROR", 0, "cloud ID discovery is temporarily unavailable")
	}
	if response.StatusCode != http.StatusOK || tooLarge {
		return "", errx.Internal("cloud ID discovery failed safely")
	}
	var payload struct {
		CloudID string `json:"cloudId"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &payload); err != nil {
		return "", errx.Internal("cloud ID discovery returned invalid JSON")
	}
	if err := atlassian.ValidateCloudID(payload.CloudID); err != nil {
		return "", errx.Internal("cloud ID discovery returned an invalid cloud ID")
	}
	return payload.CloudID, nil
}
