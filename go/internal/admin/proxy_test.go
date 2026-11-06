package admin

import "testing"

func TestAccessGrantCovers(t *testing.T) {
	if !accessGrantCovers("tenant", nil, []string{"shop-a", "shop-b"}) {
		t.Fatal("tenant scope must cover every shop")
	}
	if !accessGrantCovers("shops", []string{"shop-a", "shop-b"}, []string{"shop-a"}) {
		t.Fatal("shop scope must cover a subset")
	}
	if accessGrantCovers("shops", []string{"shop-a"}, []string{"shop-a", "shop-b"}) {
		t.Fatal("shop scope must not cover a shop it lacks")
	}
	if accessGrantCovers("shops", nil, []string{"shop-a"}) {
		t.Fatal("empty shop scope covers nothing")
	}
}
