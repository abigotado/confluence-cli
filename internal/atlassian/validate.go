// Package atlassian validates non-secret Atlassian Cloud identifiers shared
// by profile persistence and HTTP endpoint construction.
package atlassian

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
)

const (
	MaxCloudIDLength   = 128
	MaxNumericIDLength = 32
)

var cloudIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidateSite accepts only an exact lowercase Atlassian Cloud tenant origin.
func ValidateSite(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid Atlassian site: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil || parsed.Port() != "" || strings.Contains(parsed.Host, ":") || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || raw != "https://"+parsed.Host {
		return fmt.Errorf("site must be exactly https://<tenant>.atlassian.net with no credentials, port, path, query, or fragment")
	}
	host := parsed.Hostname()
	const suffix = ".atlassian.net"
	if host == "" || host != strings.ToLower(host) || net.ParseIP(host) != nil || !strings.HasSuffix(host, suffix) {
		return fmt.Errorf("site host must be a lowercase Atlassian Cloud tenant")
	}
	tenant := strings.TrimSuffix(host, suffix)
	if tenant == "" || strings.Contains(tenant, ".") || !validDNSLabel(tenant) {
		return fmt.Errorf("site must contain exactly one valid tenant label before atlassian.net")
	}
	return nil
}

// ValidateEmail accepts a plain address suitable for Atlassian Basic auth.
func ValidateEmail(email string) error {
	if email == "" || len(email) > 254 || strings.TrimSpace(email) != email || strings.ContainsAny(email, "\x00\r\n\t ") || strings.Count(email, "@") != 1 {
		return fmt.Errorf("email must be a plain address without whitespace")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return fmt.Errorf("email must be a plain RFC 5322 address")
	}
	parts := strings.SplitN(email, "@", 2)
	if parts[0] == "" || !validEmailDomain(parts[1]) {
		return fmt.Errorf("email must contain a local part and domain")
	}
	return nil
}

// ValidateCloudID validates the tenant identifier used by the scoped gateway.
func ValidateCloudID(cloudID string) error {
	if cloudID == "" || len(cloudID) > MaxCloudIDLength || !cloudIDPattern.MatchString(cloudID) {
		return fmt.Errorf("cloud ID must be 1-%d ASCII letters, digits, dash, or underscore", MaxCloudIDLength)
	}
	return nil
}

func validEmailDomain(domain string) bool {
	labels := strings.Split(strings.ToLower(domain), ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !validDNSLabel(label) {
			return false
		}
	}
	return true
}

func validDNSLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, character := range label {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
