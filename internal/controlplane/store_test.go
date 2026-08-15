package controlplane

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeMethods(t *testing.T) {
	methods, err := normalizeMethods([]string{"post", "GET", "post"})
	if err != nil {
		t.Fatalf("normalizeMethods: %v", err)
	}
	if !reflect.DeepEqual(methods, []string{"GET", "POST"}) {
		t.Fatalf("methods = %#v", methods)
	}
	if _, err = normalizeMethods([]string{"TRACE"}); err == nil {
		t.Fatal("TRACE should be rejected")
	}
}

func TestNormalizeContentTypes(t *testing.T) {
	types, err := normalizeContentTypes([]string{"Application/JSON; charset=utf-8", "text/xml"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !reflect.DeepEqual(types, []string{"application/json", "text/xml"}) {
		t.Fatalf("types = %#v", types)
	}
	if _, err = normalizeContentTypes([]string{"application/*"}); err == nil {
		t.Fatal("wildcard should be rejected")
	}
}

func TestUpstreamAllowlist(t *testing.T) {
	store := &Store{upstreamCIDRs: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}}
	if !store.addressAllowed(netip.MustParseAddr("192.168.40.30")) {
		t.Fatal("allowed upstream was rejected")
	}
	if store.addressAllowed(netip.MustParseAddr("203.0.113.10")) {
		t.Fatal("outside upstream was allowed")
	}
}

func TestValidationHelpers(t *testing.T) {
	regex, err := normalizeUnprotectedRouteRegex(`^/(swagger|docs)(/.*)?$|\.(js|ya?ml|jpe?g)$`)
	if err != nil || regex == "" {
		t.Fatalf("valid unprotected route regex rejected: %q, %v", regex, err)
	}
	for _, value := range []string{"[", "^/docs$\n^/other$", strings.Repeat("a", 2049)} {
		if _, err = normalizeUnprotectedRouteRegex(value); err == nil {
			t.Fatalf("invalid unprotected route regex accepted: %q", value)
		}
	}

	for _, value := range []string{"v1", "stable", "2026.08", "release_candidate", "版本一"} {
		if !validVersion(value) {
			t.Fatalf("valid version rejected: %q", value)
		}
	}
	for _, value := range []string{"", "release candidate", "release\tcandidate", "release\ncandidate", string([]byte{0xff})} {
		if validVersion(value) {
			t.Fatalf("invalid version accepted: %q", value)
		}
	}

	for _, value := range []string{"rtp:v1:invoke", "qrph:v2-beta:read"} {
		if !permissionPattern.MatchString(value) {
			t.Fatalf("valid permission rejected: %s", value)
		}
	}
	for _, value := range []string{"", "RTP:v1:invoke", "rtp/invoke"} {
		if permissionPattern.MatchString(value) {
			t.Fatalf("invalid permission accepted: %s", value)
		}
	}
	if !validPath("/health-checks") || validPath("health") || validPath("/health\nunsafe") {
		t.Fatal("path validation mismatch")
	}
	if !validOptionalPath("") || !validOptionalPath("/ready") || validOptionalPath("ready") {
		t.Fatal("optional path validation mismatch")
	}
}

func TestMaskPrefix(t *testing.T) {
	if got := maskPrefix("123456789012"); got != "1234********" {
		t.Fatalf("mask = %q", got)
	}
}
