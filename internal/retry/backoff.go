package retry

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

type TunnelRetryState struct {
	Count         int       `json:"retry_count"`
	MaxRetries    int       `json:"max_retries"`
	BaseInterval  time.Duration `json:"base_interval"`
	NextRetryAt   time.Time `json:"next_retry_at"`
	LastAttemptAt time.Time `json:"last_attempt_at"`
	IsRetrying    bool      `json:"is_retrying"`
}

type Controller struct {
	mu     sync.Mutex
	states map[string]*TunnelRetryState
}

func NewController() *Controller {
	return &Controller{
		states: make(map[string]*TunnelRetryState),
	}
}

func (c *Controller) GetState(hash string) *TunnelRetryState {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.states[hash]
	if !exists {
		return &TunnelRetryState{
			MaxRetries:   10,
			BaseInterval: 5 * time.Second,
		}
	}
	// Return copy
	cp := *state
	return &cp
}

func (c *Controller) RecordFailure(hash string, maxRetries int, baseIntervalSeconds int) (shouldRetry bool, delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if maxRetries <= 0 {
		maxRetries = 10
	}
	if baseIntervalSeconds <= 0 {
		baseIntervalSeconds = 5
	}
	baseInterval := time.Duration(baseIntervalSeconds) * time.Second

	state, exists := c.states[hash]
	if !exists {
		state = &TunnelRetryState{
			MaxRetries:   maxRetries,
			BaseInterval: baseInterval,
		}
		c.states[hash] = state
	}

	state.Count++
	state.LastAttemptAt = time.Now()
	state.MaxRetries = maxRetries

	if maxRetries > 0 && state.Count > maxRetries {
		state.IsRetrying = false
		return false, 0
	}

	// Exponential backoff: base * 2^(count-1), capped at 2 minutes
	expFactor := math.Pow(2, float64(state.Count-1))
	delaySecs := float64(baseIntervalSeconds) * expFactor
	if delaySecs > 120 {
		delaySecs = 120
	}

	// Add 10% random jitter
	jitter := (rand.Float64()*0.2 - 0.1) * delaySecs
	finalDelay := time.Duration((delaySecs+jitter)*float64(time.Second))

	state.NextRetryAt = time.Now().Add(finalDelay)
	state.IsRetrying = true
	return true, finalDelay
}

func (c *Controller) RecordSuccess(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.states, hash)
}

func (c *Controller) Reset(hash string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.states, hash)
}
