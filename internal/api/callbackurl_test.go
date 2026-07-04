package api

import (
	"net/netip"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback", "127.0.0.1", true},
		{"rfc1918-10", "10.0.0.1", true},
		{"rfc1918-192", "192.168.0.1", true},
		{"rfc1918-172", "172.16.0.1", true},
		{"link-local-metadata", "169.254.169.254", true},
		{"unspecified", "0.0.0.0", true},
		{"public", "8.8.8.8", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, err := netip.ParseAddr(c.ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", c.ip, err)
			}
			if got := isBlockedIP(addr); got != c.want {
				t.Errorf("isBlockedIP(%q) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}

func TestValidateCallbackURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"loopback IP literal", "https://127.0.0.1/hook", true},
		{"cloud metadata endpoint", "https://169.254.169.254/latest/meta-data", true},
		{"insecure http rejected by default", "http://8.8.8.8/hook", true},
		{"public https IP literal allowed", "https://8.8.8.8/hook", false},
		{"empty", "", true},
		{"not a url", "not-a-url", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCallbackURL(c.url)
			if (err != nil) != c.wantErr {
				t.Errorf("validateCallbackURL(%q) error = %v, wantErr %v", c.url, err, c.wantErr)
			}
		})
	}
}
