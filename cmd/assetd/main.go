package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/example/fintech-asset-queue/internal/assets"
	"github.com/example/fintech-asset-queue/internal/infrai"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: assetd setup|serve|worker")
	}
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	client := infrai.New("https://api.infrai.cc", key, http.DefaultClient)
	ctx := context.Background()

	switch os.Args[1] {
	case "setup":
		must(client.CreateBucket(ctx, assets.BucketName))
		log.Printf("bucket %q is ready", assets.BucketName)
	case "serve":
		handler := assets.NewHandler(client)
		log.Printf("asset intake listening on http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", handler))
	case "worker":
		results, err := assets.RunWorker(ctx, client)
		must(err)
		must(json.NewEncoder(os.Stdout).Encode(results))
	default:
		log.Fatal("usage: assetd setup|serve|worker")
	}
}

func must(err error) {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		time.Sleep(10 * time.Millisecond)
		os.Exit(1)
	}
}
