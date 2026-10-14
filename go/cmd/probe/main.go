// Probe reports readiness without printing internal responses or configuration.
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	c := http.Client{Timeout: 2 * time.Second}
	r, e := c.Get("http://127.0.0.1:8080/healthz")
	if e != nil {
		os.Exit(1)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		os.Exit(1)
	}
}
