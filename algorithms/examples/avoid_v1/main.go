package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

var version = "dev"

func main() {
	go func() {
		http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		})
		_ = http.ListenAndServe(":7070", nil)
	}()

	host, _ := os.Hostname()
	for {
		fmt.Printf("[algo] version=%s host=%s ts=%s\n",
			version, host, time.Now().Format(time.RFC3339))
		time.Sleep(2 * time.Second)
	}
}
