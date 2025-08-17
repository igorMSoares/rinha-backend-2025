package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/balancer"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/dispatcher"
	httpClient "github.com/igorMSoares/rinha-de-backend-2025/internal/http"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/server"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/utils"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/worker"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	godotenv.Load()

	redisPoolSize, _ := strconv.Atoi(utils.Getenv("DISPATCHER_REDIS_POOL", "50"))
	redisClient := redis.NewClient(&redis.Options{
		Addr:            "redis:6379",
		PoolSize:        redisPoolSize,
		MinIdleConns:    int(0.2 * float64(redisPoolSize)), // 20% of pool
		MaxIdleConns:    int(0.4 * float64(redisPoolSize)), // 40% of pool
		ConnMaxLifetime: 0,
		ConnMaxIdleTime: 5 * time.Minute,
		PoolTimeout:     4 * time.Second,
		ReadTimeout:     4 * time.Second,
		WriteTimeout:    4 * time.Second,
		DialTimeout:     20 * time.Second,
	})
	err := redisClient.Ping(context.Background()).Err()
	if err != nil {
		log.Fatalf("Failed to connect to redis client: %v\n", err)
	}

	resultsHandler := worker.NewResultsHandler(redisClient)

	server := server.NewServer("processed", redisClient, resultsHandler)
	go server.Start()

	defaultCfg := &httpClient.HostCfg{
		Addr:     "payment-processor-default:8080",
		Endpoint: "http://payment-processor-default:8080/payments",
	}

	fallbackCfg := &httpClient.HostCfg{
		Addr:     "payment-processor-fallback:8080",
		Endpoint: "http://payment-processor-fallback:8080/payments",
	}

	costWeight, _ := strconv.ParseFloat(utils.Getenv("COST_WEIGHT", "0.5"), 64)
	latencyDuration, _ := time.ParseDuration(utils.Getenv("LATENCY_LIMIT", "100ms"))
	latencyThreshold := latencyDuration.Nanoseconds()

	loadBalancer := balancer.NewLoadBalancer(
		defaultCfg,
		fallbackCfg,
		costWeight,
		latencyThreshold,
	)

	workDispatcher := dispatcher.NewWorkDispatcher(loadBalancer, redisClient, resultsHandler)

	workDispatcher.Start()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	log.Println("Received shutdown signal")
}
