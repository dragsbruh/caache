package main

import (
	"context"
	"encoding/json"
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

var imageSizes = []int{0, 1024, 512, 256, 128}
var defaultAddr = ":80"
var httpClient = &http.Client{
	Timeout: time.Second * 60, // this can be slow on certain images if theyre large, so i made it a minute
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	godotenv.Load()

	if os.Getenv("DEV") != "" {
		log.Logger = log.Logger.Output(zerolog.NewConsoleWriter()).Level(zerolog.DebugLevel)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = defaultAddr
	}

	imagesDir := os.Getenv("IMAGES_DIR")
	if imagesDir == "" {
		log.Fatal().Msg("IMAGES_DIR is not set")
	}

	sf := singleflight.Group{}

	if err := os.MkdirAll(filepath.Join(imagesDir, "release"), 0755); err != nil {
		log.Fatal().Err(err).Msg("couldnt create release dir")
	}
	if err := os.MkdirAll(filepath.Join(imagesDir, "release-group"), 0755); err != nil {
		log.Fatal().Err(err).Msg("couldnt create release-group dir")
	}

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
	router := http.NewServeMux()

	router.HandleFunc("GET /{record_type}/{mbid}/{size}", func(w http.ResponseWriter, r *http.Request) {
		recordType := r.PathValue("record_type")
		sizeStr := r.PathValue("size")
		mbid := r.PathValue("mbid")

		if recordType != "release" && recordType != "release-group" {
			w.WriteHeader(400)
			w.Write([]byte("expected `release` or `release-group` record type"))
			return
		}

		switch sizeStr {
		case "128", "256", "512", "1024", "original":
		default:
			w.WriteHeader(400)
			w.Write([]byte("expected `128`, `256`, `512`, `1024` or `original` size query"))
			return
		}

		imagePath := PathOf(imagesDir, recordType, mbid, sizeStr)

		file, err := os.Open(imagePath)
		if err == nil {
			defer file.Close()

			PutImageHeaders(w)
			if _, err := io.Copy(w, file); err != nil {
				log.Err(err).Str("file", imagePath).Msg("error streaming file")
			}
			return
		} else if !os.IsNotExist(err) {
			log.Err(err).Str("file", imagePath).Msg("error opening file")
			w.WriteHeader(500)
			return
		}

		statusCode, err, _ := sf.Do(fmt.Sprint(recordType, mbid), func() (any, error) {
			_, err := os.Stat(imagePath)
			if err == nil {
				return nil, nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("error stat-ing sf'd image: %w", err)
			}

			if err := os.MkdirAll(filepath.Dir(imagePath), 0755); err != nil {
				return nil, fmt.Errorf("error making directory: %w", err)
			}

			statusCode, err := FetchImage(ctx, imagesDir, recordType, mbid)
			if err != nil {
				return nil, fmt.Errorf("error fetching images: %w", err)
			}

			return statusCode, nil
		})
		if err != nil {
			log.Err(err).Str("file", imagePath).Msg("error in cache miss")
			w.WriteHeader(500)
			return
		}

		if statusCode != nil {
			log.Warn().Int("status_code", statusCode.(int)).Msg("forwarding caa's non-200 status code")
			w.WriteHeader(statusCode.(int))
			return
		}

		file, err = os.Open(imagePath)
		if err != nil {
			log.Err(err).Str("file", imagePath).Msg("cannot access image in miss")
			w.WriteHeader(500)
			return
		}
		defer file.Close()

		PutImageHeaders(w)
		io.Copy(w, file)
	})

	router.HandleFunc("GET /cache", func(w http.ResponseWriter, r *http.Request) {
		releaseEntries, err := os.ReadDir(filepath.Join(imagesDir, "release"))
		if err != nil {
			log.Err(err).Msg("couldnt read release directory")
			w.WriteHeader(500)
			return
		}
		releaseGroupEntries, err := os.ReadDir(filepath.Join(imagesDir, "release-group"))
		if err != nil {
			log.Err(err).Msg("couldnt read release-group directory")
			w.WriteHeader(500)
			return
		}

		cacheStatus := map[string][]string{}
		for _, release := range releaseEntries {
			cacheStatus["release"] = append(cacheStatus["release"], release.Name())
		}
		for _, release := range releaseGroupEntries {
			cacheStatus["release-group"] = append(cacheStatus["release-group"], release.Name())
		}

		w.Header().Set("content-type", "application/json")
		w.Header().Set("access-control-allow-origin", "*")

		if err := json.NewEncoder(w).Encode(cacheStatus); err != nil {
			log.Err(err).Msg("error encoding cache status json")
			return
		}
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		PutCorsHeaders(w)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		router.ServeHTTP(w, r)
	})
}

// sets image/jpeg and cache headers
func PutImageHeaders(w http.ResponseWriter) {
	w.Header().Set("content-type", "image/jpeg")
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")
}

func PutCorsHeaders(w http.ResponseWriter) {
	w.Header().Set("access-control-allow-origin", "*")
	w.Header().Set("access-control-allow-methods", "GET, OPTIONS")
	w.Header().Set("access-control-max-age", "86400")
}

// saves all sizes, original as original in imagesDir/recordType/mbid/<size>.jpg
// will still do it if files already exist, idk if its a good thing or not
// saves initially to tmp file so should be good if we have to exit or smth
// returns status code or nil as first return arg
func FetchImage(ctx context.Context, imagesDir, recordType, mbid string) (any, error) {
	url := fmt.Sprintf("https://coverartarchive.org/%s/%s/front", recordType, mbid)
	log.Debug().Str("url", url).Str("mbid", mbid).Msg("fetching image from upstream")

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error getting caa image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Debug().Str("mbid", mbid).Int("status_code", resp.StatusCode).Msg("received non-200 status code from upstream")
		return resp.StatusCode, nil
	}

	log.Debug().Str("mbid", mbid).Msg("decoding image")
	img, err := imaging.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error decoding image: %w", err)
	}

	for _, size := range imageSizes {
		imagePath := PathOf(imagesDir, recordType, mbid, size)

		file, err := os.Create(imagePath + ".tmp")
		if err != nil {
			return nil, fmt.Errorf("error creating temp file: %w", err)
		}

		log.Debug().Str("mbid", mbid).Int("size", size).Msg("encoding to new resolution")

		if size != 0 { // 0 = save original
			// this might appear dangerous but since order of sizes is decreasing this is actually more performant on high load
			img = imaging.Resize(img, size, size, imaging.Lanczos)
		}

		if err := imaging.Encode(file, img, imaging.JPEG); err != nil {
			file.Close()
			return nil, fmt.Errorf("error encoding image: %w", err)
		}

		file.Close()
	}

	log.Debug().Str("mbid", mbid).Msg("renaming tmp files")
	for _, size := range imageSizes {
		imagePath := PathOf(imagesDir, recordType, mbid, size)

		if err := os.Rename(imagePath+".tmp", imagePath); err != nil {
			return nil, fmt.Errorf("error renaming tmp to real: %w", err)
		}
	}

	return nil, nil
}

// replaces any size `0` with `original` and returns `imagesDir/recordType/mbid/size.jpg`
func PathOf[T int | string](imagesDir, recordType, mbid string, size T) string {
	sizeStr := "original"

	switch v := any(size).(type) {
	case string:
		if v != "0" {
			sizeStr = v
		}
	case int:
		if v != 0 {
			sizeStr = fmt.Sprint(v)
		}
	default:
	}

	return filepath.Join(imagesDir, recordType, mbid, sizeStr+".jpg")
}
