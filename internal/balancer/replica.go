package balancer

import (
	"sync"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/breaker"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/http"
)

type ReplicaStats struct {
	sync.RWMutex
	LatencyAlpha float64
	LatencyBeta  float64
}

type Replica struct {
	Type           http.HostType
	Stats          *ReplicaStats
	CircuitBreaker *breaker.CircuitBreaker
}
