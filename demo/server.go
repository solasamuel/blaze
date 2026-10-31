//go:build ignore

// Demo target for screenshots and recordings. Latency is long-tailed (most
// requests ~20-60ms, a few hundred ms) and ~1% of requests hang for 3s so a run
// with --timeout 1s shows a timeout breakdown.
//
//	go run demo/server.go            # listens on :8080
package main

import (
	"log"
	"math/rand"
	"net/http"
	"time"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch p := rand.Float64(); {
		case p < 0.01:
			time.Sleep(3 * time.Second)
		case p < 0.05:
			time.Sleep(time.Duration(150+rand.Intn(350)) * time.Millisecond)
		default:
			time.Sleep(time.Duration(20+rand.Intn(40)) * time.Millisecond)
		}
		w.Write([]byte("ok\n"))
	})
	log.Println("demo target on http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
