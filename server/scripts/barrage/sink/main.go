package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := "127.0.0.1:9999"
	if len(os.Args) > 1 {
		addr = os.Args[1]
	}
	log.Fatal(http.ListenAndServe(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
}
