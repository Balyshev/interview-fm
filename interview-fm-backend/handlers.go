package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

func (s *service) resizeHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("Expecting POST request"))
			return
		}

		async := r.URL.Query().Get("async") == "true"

		request := resizeRequest{}
		err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&request)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Failed to parse request"))
			return
		}

		results, err := s.processResizes(r.Context(), request, async)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Failed to process request"))
			return
		}

		data, err := json.Marshal(results)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Failed to marshal response"))
			return
		}

		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(data)
	})
}

func (s *service) getImageHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.String()

		// 1. Cache hit — самый быстрый путь
		if data, ok := s.cache.Get(key); ok {
			w.Header().Set("content-type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.Write(data.([]byte))
			return
		}

		// 2. Проверяем, обрабатывается ли изображение
		s.mu.Lock()
		job, exists := s.processing[key]
		s.mu.Unlock()

		if !exists {
			// Финальная проверка кеша (закрывает race между шагами 1 и 2)
			if data, ok := s.cache.Get(key); ok {
				w.Header().Set("content-type", "image/jpeg")
				w.WriteHeader(http.StatusOK)
				w.Write(data.([]byte))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		log.Printf("waiting for image %s", key)

		// 3. Ждём завершения обработки с таймаутом
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		select {
		case <-job.done:
			if job.err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("Failed to process image"))
				return
			}

			// Повторно проверяем кеш
			data, ok := s.cache.Get(key)
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			w.Header().Set("content-type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.Write(data.([]byte))

		case <-ctx.Done():
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("Processing timeout"))
		}
	})
}
