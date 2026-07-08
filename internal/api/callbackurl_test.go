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

func TestIsBlockedIPAllowPrivate(t *testing.T) {
	t.Run("flag off: RFC1918 stays blocked", func(t *testing.T) {
		cases := []string{"10.0.0.1", "192.168.0.1", "172.16.0.1"}
		for _, ip := range cases {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", ip, err)
			}
			if !isBlockedIP(addr) {
				t.Errorf("isBlockedIP(%q) = false, want true when WEBHOOK_ALLOW_PRIVATE_IPS is unset", ip)
			}
		}
	})

	t.Run("flag on: RFC1918 allowed, hard-blocks unchanged", func(t *testing.T) {
		t.Setenv("WEBHOOK_ALLOW_PRIVATE_IPS", "true")

		allowed := []string{"10.0.0.1", "192.168.0.1", "172.16.0.1"}
		for _, ip := range allowed {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", ip, err)
			}
			if isBlockedIP(addr) {
				t.Errorf("isBlockedIP(%q) = true, want false when WEBHOOK_ALLOW_PRIVATE_IPS=true", ip)
			}
		}

		stillBlocked := []string{"127.0.0.1", "169.254.169.254", "0.0.0.0"}
		for _, ip := range stillBlocked {
			addr, err := netip.ParseAddr(ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", ip, err)
			}
			if !isBlockedIP(addr) {
				t.Errorf("isBlockedIP(%q) = false, want true even with WEBHOOK_ALLOW_PRIVATE_IPS=true", ip)
			}
		}
	})
}

func TestValidateCallbackURLAllowPrivate(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE_IPS", "true")

	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"rfc1918 https allowed", "https://10.0.0.1/hook", false},
		{"http scheme still rejected (separate flag)", "http://10.0.0.1/hook", true},
		{"invalid url still rejected", "not-a-url", true},
		{"empty still rejected", "", true},
		{"loopback still blocked", "https://127.0.0.1/hook", true},
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
