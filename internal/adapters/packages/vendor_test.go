package packages

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReviewedHTTPClientRejectsCredentialBearingURL(t *testing.T) {
	if _, err := ReviewedHTTPClient(
		nil,
		"https://user:password@example.com/artifact",
		5*time.Minute,
	); err == nil || !strings.Contains(err.Error(), "credential-free") {
		t.Fatalf("reviewedHTTPClient() error = %v, want credential rejection", err)
	}
}

func TestReviewedHTTPClientRejectsQueryBearingURL(t *testing.T) {
	if _, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact?token=secret",
		5*time.Minute,
	); err == nil || !strings.Contains(err.Error(), "query") {
		t.Fatalf("ReviewedHTTPClient() error = %v, want query rejection", err)
	}
}

func TestReviewedHTTPClientRejectsCrossOriginRedirect(t *testing.T) {
	client, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("reviewedHTTPClient(): %v", err)
	}
	request := &http.Request{URL: &url.URL{
		Scheme: "https",
		Host:   "cdn.example.net",
		Path:   "/artifact",
	}}
	if err := client.CheckRedirect(request, nil); err == nil ||
		!strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("CheckRedirect() error = %v, want cross-origin rejection", err)
	}
}

func TestReviewedHTTPClientRejectsCredentialBearingRedirect(t *testing.T) {
	client, err := ReviewedHTTPClient(
		nil,
		"https://example.com/artifact",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("ReviewedHTTPClient(): %v", err)
	}
	request := &http.Request{URL: &url.URL{
		Scheme: "https",
		Host:   "example.com",
		User:   url.UserPassword("token", "secret"),
		Path:   "/artifact",
	}}
	if err := client.CheckRedirect(request, nil); err == nil ||
		!strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf(
			"CheckRedirect() error = %v, want credential-bearing redirect rejection",
			err,
		)
	}
}

func TestReviewedReleaseHTTPClientAllowsOnlyThreeCredentialFreeHTTPSRedirects(
	t *testing.T,
) {
	client, err := reviewedReleaseHTTPClient(
		nil,
		"https://github.com/example/tool/releases/download/v1/tool.tar.gz",
		5*time.Minute,
	)
	if err != nil {
		t.Fatalf("reviewedReleaseHTTPClient(): %v", err)
	}
	redirect := &http.Request{
		URL: &url.URL{
			Scheme:   "https",
			Host:     "release-assets.githubusercontent.com",
			Path:     "/tool.tar.gz",
			RawQuery: "signature=temporary",
		},
		Header: http.Header{
			"Authorization":       {"Bearer secret"},
			"Cookie":              {"session=secret"},
			"Proxy-Authorization": {"Basic secret"},
		},
	}
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 3),
	); err != nil {
		t.Fatalf("third redirect rejected: %v", err)
	}
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"Proxy-Authorization",
	} {
		if redirect.Header.Get(header) != "" {
			t.Fatalf("redirect retained %s", header)
		}
	}
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 4),
	); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("fourth redirect error = %v, want bounded rejection", err)
	}

	redirect.URL.User = url.UserPassword("user", "password")
	if err := client.CheckRedirect(
		redirect,
		make([]*http.Request, 1),
	); err == nil || !strings.Contains(err.Error(), "credential-free") {
		t.Fatalf("credentialed redirect error = %v", err)
	}
}
