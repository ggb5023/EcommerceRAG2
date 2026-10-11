package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ggb5023/EcommerceRAG/go/internal/facts"
)

func main() {
	addr := os.Getenv("FACTS_FAKE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}
	version := os.Getenv("FACTS_FAKE_VERSION")
	fault := os.Getenv("FACTS_FAKE_FAULT")
	if err := http.ListenAndServe(addr, facts.NewFakeHandler(version, fault)); err != nil {
		log.Fatal("facts fake stopped")
	}
}
