package gateway

import "testing"

func TestDeploymentFormD1BuiltInEdge(t *testing.T) {
	if err := validateDeploymentForm("/srv/frontend", "0.0.0.0:8443", ""); err != nil {
		t.Fatalf("D1 must be accepted: %v", err)
	}
}

func TestDeploymentFormD2FrontProxy(t *testing.T) {
	if err := validateDeploymentForm("", "127.0.0.1:8080", ""); err != nil {
		t.Fatalf("D2 on loopback must be accepted: %v", err)
	}
}

func TestDeploymentFormD3LocalDev(t *testing.T) {
	if err := validateDeploymentForm("", "", ""); err != nil {
		t.Fatalf("an empty address defaults to loopback: %v", err)
	}
}

func TestDeploymentFormD4IsRejected(t *testing.T) {
	if err := validateDeploymentForm("/srv/frontend", "127.0.0.1:8080", "proxy.internal"); err == nil {
		t.Fatal("built-in edge plus a trusted proxy must be rejected")
	}
}

func TestDeploymentFormPlainHTTPMustStayOnLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", ":8080", "192.0.2.10:8080", "example.test:8080"} {
		if err := validateDeploymentForm("", addr, ""); err == nil {
			t.Fatalf("plain HTTP on %q must be rejected", addr)
		}
	}
}
