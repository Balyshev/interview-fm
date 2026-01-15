package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"time"

	jpgresize "github.com/nfnt/resize"
)

type Job struct {
	done chan struct{}
	err  error
}

func (s *service) processResizes(
	ctx context.Context,
	request resizeRequest,
	async bool,
) ([]resizeResult, error) {

	results := make([]resizeResult, 0, len(request.URLs))

	for _, url := range request.URLs {
		result := resizeResult{}

		key, cached, err := s.ensureImage(
			ctx,
			url,
			request.Width,
			request.Height,
			async,
		)

		if err != nil {
			log.Printf("failed to resize %s: %v", url, err)
			result.Result = failure
		} else {
			result.URL = proto + hostport + key
			result.Result = success
			result.Cached = cached
		}

		results = append(results, result)
	}

	return results, nil
}

func fetchAndResize(ctx context.Context, url string, width, height uint) ([]byte, error) {
	data, err := fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	return resize(data, width, height)
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	log.Print("fetching ", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("non-200 status: %d", resp.StatusCode)
	}

	return io.ReadAll(io.LimitReader(resp.Body, 15*1024*1024))
}

func resize(data []byte, width, height uint) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	resized := jpgresize.Resize(width, height, img, jpgresize.Lanczos3)

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	if err := jpeg.Encode(w, resized, nil); err != nil {
		return nil, err
	}
	if err := w.Flush(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func genID(url string) string {
	hash := sha256.Sum256([]byte(url))
	return base64.URLEncoding.EncodeToString(hash[:])
}

func (s *service) ensureImage(
	ctx context.Context,
	url string,
	width, height uint,
	async bool,
) (string, bool, error) {

	key := "/v1/image/" + genID(url) + ".jpeg"

	if _, ok := s.cache.Get(key); ok {
		return key, true, nil
	}

	s.mu.Lock()
	job, exists := s.processing[key]
	if !exists {
		job = &Job{done: make(chan struct{})}
		s.processing[key] = job

		go func() {
			defer func() {
				close(job.done)
				if r := recover(); r != nil {
					log.Printf("panic in resize goroutine for %s: %v", url, r)
				}
			}()

			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			data, err := fetchAndResize(bgCtx, url, width, height)

			s.mu.Lock()
			if err == nil {
				s.cache.Add(key, data)
			}
			job.err = err
			delete(s.processing, key)
			s.mu.Unlock()
		}()
	}
	s.mu.Unlock()

	if async {
		return key, false, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	select {
	case <-job.done:
		return key, false, job.err
	case <-waitCtx.Done():
		return "", false, fmt.Errorf("resize timeout")
	}
}
