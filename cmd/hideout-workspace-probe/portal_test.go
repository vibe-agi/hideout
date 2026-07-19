package main

import "testing"

func TestPortalAdvertisedAddressUsesGuestHostAndBoundPort(t *testing.T) {
	address, err := portalAdvertisedAddress("127.0.0.1:43123", "host.lima.internal")
	if err != nil {
		t.Fatal(err)
	}
	if address != "host.lima.internal:43123" {
		t.Fatalf("advertised address = %q", address)
	}
	if _, err := portalAdvertisedAddress("127.0.0.1:43123", "host/name"); err == nil {
		t.Fatal("invalid guest host accepted")
	}
}
