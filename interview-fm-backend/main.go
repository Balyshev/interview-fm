package main

import (
	"log"
	"net/http"
	"sync"

	lru "github.com/hashicorp/golang-lru"
)

type service struct {
	cache      *lru.Cache
	processing map[string]*Job
	mu         sync.Mutex
}

type resizeRequest struct {
	URLs   []string `json:"urls"`
	Width  uint     `json:"width"`
	Height uint     `json:"height"`
}

type resizeResult struct {
	Result string `json:"result"`
	URL    string `json:"url,omitempty"`
	Cached bool   `json:"cached"`
}

const (
	proto    = "http://"
	hostport = "localhost:8080"
	success  = "success"
	failure  = "failure"
)

func main() {
	cache, err := lru.New(1024)
	if err != nil {
		log.Panicf("failed to create cache: %v", err)
	}

	svc := &service{
		cache:      cache,
		processing: make(map[string]*Job),
	}

	mux := http.NewServeMux()
	mux.Handle("/v1/resize", svc.resizeHandler())
	mux.Handle("/v1/image/", svc.getImageHandler())

	log.Print("Listening on ", hostport)
	panic(http.ListenAndServe(hostport, mux))
}
