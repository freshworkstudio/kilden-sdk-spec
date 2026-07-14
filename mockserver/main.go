// The Kilden mock capture server: a stand-in for ingest.kilden.io that every
// server SDK's CI runs against. It validates /capture requests with the same
// rules production enforces (stricter where SPEC.md is stricter), evaluates
// /decide with the frozen flag hashing, records everything it accepts, and
// simulates failures on demand. Zero dependencies.
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	flag.Parse()

	s := NewServer()
	log.Printf("kilden mock capture server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, s))
}
