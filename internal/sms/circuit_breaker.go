package sms

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is OPEN; routing to fallback")

type State string

const (
	StateClosed   State = "CLOSED"    // Normal operation
	StateOpen     State = "OPEN"      // Tripped; traffic diverted to fallback
	StateHalfOpen State = "HALF_OPEN" // Probing recovery
)

// CircuitBreaker provides automated failover from primary to fallback providers.
type CircuitBreaker struct {
	mu               sync.RWMutex
	failureThreshold int
	cooldownDuration time.Duration
	failureCount     int
	lastFailureTime  time.Time
	state            State
}

// NewCircuitBreaker creates a circuit breaker with a given failure threshold and cooldown.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: threshold,
		cooldownDuration: cooldown,
		state:            StateClosed,
	}
}

// State returns current circuit status.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateOpen && time.Since(cb.lastFailureTime) > cb.cooldownDuration {
		cb.state = StateHalfOpen
	}
	return cb.state
}

// Execute wraps an operation, tracking successes and tripping upon consecutive failures.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	currentState := cb.State()
	if currentState == StateOpen {
		return ErrCircuitOpen
	}

	err := fn()
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failureCount++
		cb.lastFailureTime = time.Now()
		if cb.failureCount >= cb.failureThreshold {
			cb.state = StateOpen
		}
		return err
	}

	// Success resets the circuit
	cb.failureCount = 0
	cb.state = StateClosed
	return nil
}
