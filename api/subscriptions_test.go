package api

import (
	"strings"
	"testing"
)

func TestUnavailableProfileCarriesReason(t *testing.T) {
	profile := string(unavailableProfile("订阅流量已耗尽"))
	for _, want := range []string{"订阅流量已耗尽", "type: socks5", "proxy-groups", "rules"} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
	if strings.Count(profile, "订阅流量已耗尽") != 2 {
		t.Fatal("reason must appear in both the node name and the group reference")
	}
}
