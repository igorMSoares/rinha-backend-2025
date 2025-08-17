package breaker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/igorMSoares/rinha-de-backend-2025/internal/http"
)

type CircuitState int

const (
	Closed CircuitState = iota
	Open
	HalfOpen
)

var (
	ErrCircuitOpen = errors.New("Circuit Breaker is open")
)

type CircuitBreaker struct {
	sync.RWMutex

	state       *CircuitBreakerState
	CircuitOpen atomic.Bool
}

type CircuitBreakerCfg struct {
	RecoveryTimeout  time.Duration
	RecoveryAttempts int // fechar circuito após este número de tentativas bem sucedidas
	FailureThreshold int // abrir circuito após este número de falhas
}

func NewCircuitBreaker(cfg *CircuitBreakerCfg) *CircuitBreaker {
	return &CircuitBreaker{
		state: NewCircuitBreakerState(cfg),
	}
}

func (cb *CircuitBreaker) AllowRequest() bool {
	state := cb.state.GetCircuitState()
	switch state {
	case Closed:
		return true
	case HalfOpen:
		return true
	case Open:
		return cb.state.TrySetHalfOpenState()
	default:
		return false
	}
}

func (cb *CircuitBreaker) updateState(success bool) {
	switch cb.state.GetCircuitState() {
	case Closed:
		if success {
			return
		}

		cb.state.TryIncrementFailure()

	case HalfOpen:
		if success {
			cb.state.TryIncrementSuccess()
			return
		}

		if cb.state.TrySetOpenState() {
			cb.CircuitOpen.Store(true)

			// log.Println("Circuit breaker is open")
			timer := time.NewTimer(time.Duration(cb.state.recoveryTimeout))
			go func() {
				<-timer.C
				// log.Println("Circuit breaker is allowing requests")
				cb.CircuitOpen.Store(false)
			}()
		}
	}
}

func (cb *CircuitBreaker) Execute(
	ctx context.Context,
	host http.HostType,
	body []byte,
	fn func(ctx context.Context, host http.HostType, body []byte) (int64, error),
) (responseTime int64, err error) {
	if !cb.AllowRequest() {
		return 0, ErrCircuitOpen
	}

	responseTime, err = fn(ctx, host, body)

	go cb.updateState(err == nil)

	return responseTime, err
}
