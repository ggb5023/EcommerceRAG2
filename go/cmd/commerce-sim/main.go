// commerce-sim serves the synthetic commerce-lab-v1 dataset for local
// development. It binds loopback only and is never part of a selfhost deploy.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ggb5023/EcommerceRAG/go/internal/commercesim"
)

func main() {
	datasetDir := flag.String("dataset", "data/synthetic/commerce-lab-v1", "commerce-lab dataset directory")
	addr := flag.String("addr", "127.0.0.1:8091", "loopback listen address")
	fault := flag.String("fault", "", "facts fault injection: unauthorized, rate_limited, server_error, timeout, malformed")
	flag.Parse()

	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("commerce-sim: invalid address: %v", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		log.Fatalf("commerce-sim: %s is not a loopback address", host)
	}
	dataset, err := commercesim.Load(*datasetDir)
	if err != nil {
		log.Fatalf("commerce-sim: %v", err)
	}
	server := commercesim.New(dataset)
	server.Fault = *fault
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Printf("READY commerce-sim %s dataset=%s tenant=%s shop=%s\n",
		*addr, dataset.Manifest.DatasetID, dataset.Manifest.TenantID, dataset.Manifest.ShopID)
	if err = httpServer.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "commerce-sim stopped:", err)
		os.Exit(1)
	}
}
