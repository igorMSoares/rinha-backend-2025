package utils

import (
	"log"
	"os"
)

func Getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}

	log.Printf("[ENV WARN] %s not defined. Using fallback value: %s", key, fallback)

	return fallback
}
