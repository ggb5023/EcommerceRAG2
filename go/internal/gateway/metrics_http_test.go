package gateway

import "testing"

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9090", "localhost:9090", "[::1]:9090"} {
		if err := validateLoopbackAddress(address); err != nil {
			t.Fatalf("loopback %s rejected: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:9090", "192.0.2.1:9090", "127.0.0.1:0", "bad"} {
		if err := validateLoopbackAddress(address); err == nil {
			t.Fatalf("non-loopback or invalid address accepted: %s", address)
		}
	}
}
