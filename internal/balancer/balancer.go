package balancer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/breaker"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/http"
	"github.com/igorMSoares/rinha-de-backend-2025/internal/utils"
	"gonum.org/v1/gonum/stat/distuv"
)

var (
	ErrAllReplicasFailed = errors.New("All replicas failed")
)

type LoadBalancer struct {
	DefaultReplica   *Replica
	FallbackReplica  *Replica
	CostWeight       float64
	latencyThreshold int64
	timeout          time.Duration
	httpClient       *http.FastHTTPClient
	circuitOpen      atomic.Bool
	circuitTimeout   time.Duration
}

func NewLoadBalancer(
	defaultCfg, fallbackCfg *http.HostCfg,
	costWeight float64,
	latencyThreshold int64,
) *LoadBalancer {
	recoveryTimeout, _ := time.ParseDuration(utils.Getenv("CB_RECOVERY_TIMEOUT", "2s"))
	recoveryAttempts, _ := strconv.Atoi(utils.Getenv("CB_RECOVERY_ATTEMPTS", "5"))
	failureThreshold, _ := strconv.Atoi(utils.Getenv("CB_FAILURE_THRESHOLD", "5"))
	timeout, _ := time.ParseDuration(utils.Getenv("PROCESSOR_REQ_TIMEOUT", "500ms"))
	circuitTimeout, _ := time.ParseDuration(utils.Getenv("LB_CIRCUIT_TIMEOUT", "500ms"))

	breakerCfg := &breaker.CircuitBreakerCfg{
		RecoveryTimeout:  recoveryTimeout,
		RecoveryAttempts: recoveryAttempts,
		FailureThreshold: failureThreshold,
	}

	if costWeight < 0.0 {
		costWeight = 0 // Cost is not relevant for the score
	}
	if costWeight >= 1.0 {
		costWeight = 0.99 // Cost is critical for the score
	}

	return &LoadBalancer{
		DefaultReplica: &Replica{
			Type: http.DefaultHost,
			Stats: &ReplicaStats{
				LatencyAlpha: 1.5, // priorizado
				LatencyBeta:  1.0,
			},
			CircuitBreaker: breaker.NewCircuitBreaker(breakerCfg),
		},
		FallbackReplica: &Replica{
			Type: http.FallbackHost,
			Stats: &ReplicaStats{
				LatencyAlpha: 1.0,
				LatencyBeta:  1.0,
			},
			CircuitBreaker: breaker.NewCircuitBreaker(breakerCfg),
		},
		CostWeight:       1.0 - costWeight,
		latencyThreshold: latencyThreshold,
		timeout:          timeout,
		circuitTimeout:   circuitTimeout,
		httpClient: http.NewFastHTTPClient(
			defaultCfg,
			fallbackCfg,
		),
	}
}

func (lb *LoadBalancer) selectReplica() *Replica {
	if lb.DefaultReplica.CircuitBreaker.CircuitOpen.Load() {
		if lb.FallbackReplica.CircuitBreaker.CircuitOpen.Load() {
			// nenhuma réplica disponível
			return nil
		}

		return lb.FallbackReplica
	} else if lb.FallbackReplica.CircuitBreaker.CircuitOpen.Load() {
		return lb.DefaultReplica
	}

	lb.DefaultReplica.Stats.RLock()
	betaDefault := distuv.Beta{
		Alpha: lb.DefaultReplica.Stats.LatencyAlpha,
		Beta:  lb.DefaultReplica.Stats.LatencyBeta,
	}
	lb.DefaultReplica.Stats.RUnlock()

	lb.FallbackReplica.Stats.RLock()
	betaFallback := distuv.Beta{
		Alpha: lb.FallbackReplica.Stats.LatencyAlpha,
		Beta:  lb.FallbackReplica.Stats.LatencyBeta,
	}
	lb.FallbackReplica.Stats.RUnlock()

	scoreDefault := betaDefault.Rand()
	scoreFallback := betaFallback.Rand() * lb.CostWeight

	if scoreDefault > scoreFallback {
		// log.Println("Selected default replica")
		return lb.DefaultReplica
	}

	// log.Println("Selected fallback replica")
	return lb.FallbackReplica
}

func (lb *LoadBalancer) UpdateLatency(stats *ReplicaStats, responseTime int64) {
	if responseTime < 0 || responseTime > lb.latencyThreshold {
		inc := 1.0
		if responseTime > lb.latencyThreshold {
			// incremento proporcional à latencia (min: 0.5, max: ~1.5)
			inc = float64((responseTime-lb.latencyThreshold)/responseTime) + 0.5
		}

		stats.Lock()
		stats.LatencyBeta += inc
		stats.Unlock()
		return
	}

	// score normalizado, quanto mais perto de 1.0 melhor (min: 0.0, max: ~0.99)
	latencyScore := math.Max(0, float64(lb.latencyThreshold-responseTime)) / float64(lb.latencyThreshold)

	// incremento proporcional ao latencyScore
	weightedAlphaIncrement := 0.1 + 0.9*latencyScore // maior o latencyScore, maior o incremento (min: 0.1, max: ~0.99)
	// também incrementa beta para equilibrar a distribuição
	weightedBetaIncrement := 0.1 + 0.4*(1-latencyScore) // maior o latencyScore, menor o incremento (min: 0.1, max: ~0.5)

	stats.Lock()
	stats.LatencyAlpha += weightedAlphaIncrement
	stats.LatencyBeta += weightedBetaIncrement
	stats.Unlock()
}

func (lb *LoadBalancer) AllowWork() bool {
	return !lb.circuitOpen.Load()
}

func (lb *LoadBalancer) openCircuit() {
	if lb.circuitOpen.Load() {
		return
	}

	lb.circuitOpen.Store(true)

	timer := time.NewTimer(lb.circuitTimeout)
	log.Printf("All external services down: load balancer stopping for %v", lb.circuitTimeout)
	go func() {
		<-timer.C
		log.Println("Load balancer is allowing requests")
		lb.circuitOpen.Store(false)
	}()
}

func (lb *LoadBalancer) MakeRequest(body []byte, replica *Replica) (http.HostType, error) {
	var r *Replica
	if replica == nil {
		r = lb.selectReplica()
		if r == nil {
			log.Println("lb.MakeRequest::ErrAllReplicasFailed")
			return http.NilHost, ErrAllReplicasFailed
		}
	} else {
		r = replica
	}

	ctx, cancel := context.WithTimeout(context.Background(), lb.timeout)
	defer cancel()

	responseTime, err := r.CircuitBreaker.Execute(
		ctx,
		r.Type,
		body,
		lb.httpClient.POST,
	)
	if err != nil {
		if errors.Is(err, http.ErrAlreadyProcessed) {
			return r.Type, err
		}

		if errors.Is(err, context.DeadlineExceeded) {
			log.Println("Request timed out")
		}

		if !errors.Is(err, breaker.ErrCircuitOpen) {
			go lb.UpdateLatency(r.Stats, -1)
		}

		// Retry com outra réplica
		host, err := lb.tryOtherReplica(r, body)
		if err != nil && errors.Is(err, ErrAllReplicasFailed) {
			lb.openCircuit()
		}

		return host, err
	}

	go lb.UpdateLatency(r.Stats, responseTime)

	return r.Type, nil
}

func (lb *LoadBalancer) swapReplica(r *Replica) *Replica {
	switch r.Type {
	case http.DefaultHost:
		return lb.FallbackReplica
	case http.FallbackHost:
		return lb.DefaultReplica
	default:
		return nil
	}
}

func (lb *LoadBalancer) tryOtherReplica(failed *Replica, body []byte) (http.HostType, error) {
	otherReplica := lb.swapReplica(failed)
	if otherReplica == nil {
		return http.NilHost, fmt.Errorf("lb.swapReplica returned nil Replica")
	}

	if !otherReplica.CircuitBreaker.AllowRequest() {
		return http.NilHost, ErrAllReplicasFailed
	}

	return lb.MakeRequest(body, otherReplica)
}
