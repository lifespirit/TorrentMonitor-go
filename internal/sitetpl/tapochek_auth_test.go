package sitetpl

import "testing"

func TestTapochekAuthRequiresLoggedInMarker(t *testing.T) {
	tmpl := DefaultTapochekTemplate()
	const marker = "login.php?logout=1"

	if tmpl.Auth.Check == nil {
		t.Fatal("Tapochek auth check is not configured")
	}
	if tmpl.Auth.Check.Success.Contains != marker {
		t.Fatalf("Tapochek auth check marker = %q, want %q", tmpl.Auth.Check.Success.Contains, marker)
	}
	if tmpl.Auth.Login == nil {
		t.Fatal("Tapochek login flow is not configured")
	}
	if tmpl.Auth.Login.Success.Contains != marker {
		t.Fatalf("Tapochek login success marker = %q, want %q", tmpl.Auth.Login.Success.Contains, marker)
	}
}
