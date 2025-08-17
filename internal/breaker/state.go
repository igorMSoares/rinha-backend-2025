package breaker

import (
	"log"
	"sync/atomic"
	"time"
)

const (
	stateBits    = 2
	failureBits  = 10
	successBits  = 10
	openedAtBits = 31 // unix seconds timestamp

	stateShift    = 0
	failureShift  = stateShift + stateBits     // 2
	successShift  = failureShift + failureBits // 12
	openedAtShift = successShift + successBits // 22

	stateMask    = (1 << stateBits) - 1    // 0b11
	failureMask  = (1 << failureBits) - 1  // 0x3FF
	successMask  = (1 << successBits) - 1  // 0x3FF
	openedAtMask = (1 << openedAtBits) - 1 // 0x3FFFFFFFFFF

	maxValidState   = 2    // 3 valid states
	maxFailureCount = 1023 // 2^10 - 1
	maxSuccessCount = 1023 // 2^10 - 1
)

type CircuitBreakerState struct {
	state            uint64 // bitmap
	failureThreshold int
	recoveryAttempts int
	recoveryTimeout  int64
}

func NewCircuitBreakerState(cfg *CircuitBreakerCfg) *CircuitBreakerState {
	cbs := &CircuitBreakerState{
		failureThreshold: cfg.FailureThreshold,
		recoveryAttempts: cfg.RecoveryAttempts,
		recoveryTimeout:  int64(cfg.RecoveryTimeout.Seconds()),
	}

	cbs.setState(int(Closed), 0, 0, 0)
	return cbs
}

func packState(state, failure, success int, openedAt int64) uint64 {
	if state > maxValidState {
		log.Panicf("invalid circuit state: %d", state)
	}

	if failure < 0 || failure > maxFailureCount {
		log.Panicf("invalid circuit breaker failure count: %d", failure)
	}

	if success < 0 || success > maxSuccessCount {
		log.Panicf("invalid circuit breaker success count: %d", success)
	}

	return (uint64(state) << stateShift) |
		(uint64(failure) << failureShift) |
		(uint64(success) << successShift) |
		(uint64(openedAt) << openedAtShift)
}

func unpackState(data uint64) (state, failure, success int, openedAt int64) {
	state = int((data >> stateShift) & stateMask)
	failure = int((data >> failureShift) & failureMask)
	success = int((data >> successShift) & successMask)
	openedAt = int64((data >> openedAtShift) & openedAtMask)
	return
}

func (cbs *CircuitBreakerState) setState(state, failureCount, successCount int, openedAt int64) {
	packed := packState(state, failureCount, successCount, openedAt)
	atomic.StoreUint64(&cbs.state, packed)
}

func (cbs *CircuitBreakerState) getState() (state, failureCount, successCount int, openedAt int64) {
	packed := atomic.LoadUint64(&cbs.state)
	return unpackState(packed)
}

func (cbs *CircuitBreakerState) GetCircuitState() CircuitState {
	state, _, _, _ := cbs.getState()
	return CircuitState(state)
}

func (cbs *CircuitBreakerState) TrySetOpenState() bool {
	oldPacked := atomic.LoadUint64(&cbs.state)
	state, failureCount, successCount, _ := unpackState(oldPacked)

	if CircuitState(state) == Open {
		return true
	}

	newPacked := packState(int(Open), failureCount, successCount, time.Now().Unix())
	if atomic.CompareAndSwapUint64(&cbs.state, oldPacked, newPacked) {
		// log.Println("CircuitBreaker::OPEN")
		return true
	}

	return false
}

func (cbs *CircuitBreakerState) TrySetHalfOpenState() bool {
	oldPacked := atomic.LoadUint64(&cbs.state)
	state, failureCount, _, openedAt := unpackState(oldPacked)

	if CircuitState(state) != Open {
		// transição inválida
		return false
	}

	if time.Now().Unix()-openedAt < cbs.recoveryTimeout {
		// transição inválida
		return false
	}

	newPacked := packState(int(HalfOpen), failureCount, 0, openedAt)
	if atomic.CompareAndSwapUint64(&cbs.state, oldPacked, newPacked) {
		// log.Println("CircuitBreaker::HALF_OPEN")
		return true
	}

	return false
}

func (cbs *CircuitBreakerState) TryIncrementSuccess() bool {
	oldPacked := atomic.LoadUint64(&cbs.state)
	state, failureCount, successCount, openedAt := unpackState(oldPacked)

	if CircuitState(state) != HalfOpen {
		// incremento só é valido se HalfOpen
		return false
	}

	newCount := min(successCount+1, maxSuccessCount)
	if newCount >= cbs.recoveryAttempts {
		state = int(Closed)
	}

	newPacked := packState(state, failureCount, newCount, openedAt)
	if atomic.CompareAndSwapUint64(&cbs.state, oldPacked, newPacked) {
		// log.Println("CircuitBreaker::CLOSED")
		return true
	}

	return false
}

func (cbs *CircuitBreakerState) TryIncrementFailure() bool {
	oldPacked := atomic.LoadUint64(&cbs.state)
	state, failureCount, successCount, openedAt := unpackState(oldPacked)

	if CircuitState(state) != Closed {
		// incremento só é valido se Closed
		return false
	}

	newCount := min(failureCount+1, maxFailureCount)
	if newCount >= cbs.failureThreshold {
		state = int(Open)
		openedAt = time.Now().Unix()
	}

	newPacked := packState(state, newCount, successCount, openedAt)
	if atomic.CompareAndSwapUint64(&cbs.state, oldPacked, newPacked) {
		return true
	}

	return false
}
