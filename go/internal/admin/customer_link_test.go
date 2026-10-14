package admin

import "testing"

func TestCustomerChatURLUsesConfiguredOrigin(t *testing.T) {
	for _, test := range []struct{ origin, slug, expected string }{
		{"https://customer.example", "my-shop", "https://customer.example/shop/my-shop"},
		{"http://127.0.0.1:5173", "my-shop", "http://127.0.0.1:5173/shop/my-shop"},
		{"https://customer.example/path", "my-shop", ""},
		{"https://customer.example?next=bad", "my-shop", ""},
		{"https://user@customer.example", "my-shop", ""},
		{"http://customer.example", "my-shop", ""},
		{"", "my-shop", ""}, {"https://customer.example", "../admin", ""},
		{"https://customer.example", "//evil.example", ""},
	} {
		if actual := customerChatURL(test.origin, test.slug, true); actual != test.expected {
			t.Fatalf("unexpected customer link for origin %q: %q", test.origin, actual)
		}
	}
	if customerChatURL("https://customer.example", "my-shop", false) != "" {
		t.Fatal("disabled customer channel must not have a trial URL")
	}
}
