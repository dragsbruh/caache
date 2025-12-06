package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/disintegration/imaging"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sync/singleflight"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	godotenv.Load()

	if os.Getenv("DEV") != "" {
		log.Logger = log.Logger.Output(zerolog.NewConsoleWriter()).Level(zerolog.DebugLevel)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":80"
	}

	imagesDir := os.Getenv("IMAGES_DIR")
	if imagesDir == "" {
		log.Fatal().Msg("IMAGES_DIR is not set")
	}

	sf := singleflight.Group{}

	server := http.Server{
		Addr:    addr,
		Handler: Router(ctx, imagesDir, &sf),
	}

	go func() {
		log.Info().Str("addr", addr).Msg("listening on addr")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Err(err).Msg("error in server")
			cancel()
		}
	}()

	<-ctx.Done()

	sctx, scancel := context.WithTimeout(ctx, time.Second*10)
	defer scancel()

	if err := server.Shutdown(sctx); err != nil {
		log.Err(err).Msg("error shutting down server")
	} else {
		log.Info().Msg("server shutdown successful")
	}
}

func Router(ctx context.Context, imagesDir string, sf *singleflight.Group) http.Handler {
	r := http.NewServeMux()

	httpClient := &http.Client{
		Timeout: time.Second * 15,
	}

	r.HandleFunc("/{record_type}/{mbid}", func(w http.ResponseWriter, r *http.Request) {
		recordType := r.PathValue("record_type")
		mbid := r.PathValue("mbid")

		if recordType != "release" && recordType != "release-group" {
			w.WriteHeader(400)
			return
		}

		imagePath := filepath.Join(imagesDir, recordType, fmt.Sprintf("%s.jpg", mbid))

		file, err := os.Open(imagePath)
		if err == nil {
			defer file.Close()

			PutHeaders(w)

			if _, err := io.Copy(w, file); err != nil {
				log.Err(err).Str("file", imagePath).Msg("error streaming file")
			}

			return
		} else if !os.IsNotExist(err) {
			log.Err(err).Str("file", imagePath).Msg("error opening file")
			w.WriteHeader(500)
			return
		}

		url := fmt.Sprintf("https://coverartarchive.org/%s/%s/front-250", recordType, mbid)
		log.Debug().Str("url", url).Msg("fetching image from upstream")

		statusCode, err, _ := sf.Do(imagePath, func() (any, error) {
			_, err := os.Stat(imagePath)
			if err == nil {
				return nil, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("error stat-ing sf'd image: %w", err)
			}

			// intended use of root context here because i dont trust requests to stay long enough sometimes, lets just cache it and move on
			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

			resp, err := httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("error getting caa image: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return resp.StatusCode, nil
			}

			if err := os.MkdirAll(filepath.Dir(imagePath), 0755); err != nil {
				return nil, fmt.Errorf("error making directory: %w", err)
			}

			img, err := imaging.Decode(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("error decoding image: %w", err)
			}

			file, err = os.Create(imagePath + ".tmp")
			if err != nil {
				return nil, fmt.Errorf("error creating temp file: %w", err)
			}
			defer file.Close()

			resized := imaging.Resize(img, 128, 128, imaging.Lanczos)

			if err := imaging.Encode(file, resized, imaging.JPEG); err != nil {
				return nil, fmt.Errorf("error encoding image: %w", err)
			}

			if err := os.Rename(imagePath+".tmp", imagePath); err != nil {
				return nil, fmt.Errorf("error renaming tmp to real: %w", err)
			}

			return nil, nil
		})

		if err != nil {
			log.Err(err).Str("url", url).Str("file", imagePath).Msg("error in cache miss")
			w.WriteHeader(500)
			return
		}

		if statusCode != nil {
			log.Debug().Int("status_code", statusCode.(int)).Msg("forwarding caa's non-200 status code")
			w.WriteHeader(statusCode.(int))
			return
		}

		file, err = os.Open(imagePath)
		if err != nil {
			log.Err(err).Str("url", url).Str("file", imagePath).Msg("cannot access image in miss")
			w.WriteHeader(500)
			return
		}

		PutHeaders(w)
		io.Copy(w, file)
	})

	return r
}

func PutHeaders(w http.ResponseWriter) {
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")

	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-allow-methods", "GET, OPTIONS")
	w.Header().Set("access-control-allow-headers", "*")
	w.Header().Set("access-control-expose-headers", "*")
	w.Header().Set("access-control-max-age", "86400")
}
