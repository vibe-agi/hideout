package main

import "testing"

func TestParseConnectMap(t *testing.T) {
	source, destination, err := parseConnectMap("1.1.1.1:443=cloudflare-dns.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if source != "1.1.1.1:443" || destination != "cloudflare-dns.com:443" {
		t.Fatalf("map=%q -> %q", source, destination)
	}
}

func TestParseConnectMapRejectsIncompleteAddresses(t *testing.T) {
	for _, value := range []string{
		"",
		"1.1.1.1:443",
		"1.1.1.1=cloudflare-dns.com:443",
		"1.1.1.1:443=cloudflare-dns.com",
		"1.1.1.1:443=cloudflare-dns.com:443=extra",
	} {
		if _, _, err := parseConnectMap(value); err == nil {
			t.Fatalf("parseConnectMap(%q) succeeded", value)
		}
	}
}
