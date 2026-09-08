package resilience_test

import (
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/fmndantas/payments/internal/resilience"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreakerFlux(t *testing.T) {
	var (
		now          = time.Now()
		anyInput     = rand.IntN(1000)
		requestFails = true
		requestCalls = 0
	)

	doRequest := func(_ int) int {
		requestCalls++
		return 1
	}

	breaker, _ := resilience.CreateCircuitBreaker(
		1,
		doRequest,
		func(_ int) bool { return requestFails },
	)

	// First failed request stays below the threshold.
	firstFailure := breaker(now, anyInput)
	assert.Equal(t, 1, requestCalls)
	assert.True(t, firstFailure.IsClosed(), "should remain closed")
	assert.Nil(t, firstFailure.OpenUntil, "closed: no OpenUntil")
	require.NotNil(t, firstFailure.RequestResult, "closed: has response")
	assert.Equal(t, 1, firstFailure.RequestResult, "response == 1")

	// Second failed request reaches the threshold and opens the circuit.
	opened := breaker(now, anyInput)
	assert.Equal(t, 2, requestCalls)
	assert.True(t, opened.IsOpen(), "should open at threshold")
	require.NotNil(t, opened.OpenUntil, "open: has OpenUntil")

	// Requests before OpenUntil are short-circuited.
	stillOpenAt := opened.OpenUntil.Add(-time.Minute)
	stillOpen := breaker(stillOpenAt, anyInput)
	assert.Equal(t, 2, requestCalls, "open: request is not called")
	assert.True(t, stillOpen.IsOpen(), "should stay open")
	assert.Equal(t, opened.OpenUntil, stillOpen.OpenUntil, "open window is unchanged")

	// A failed half-open probe reopens the circuit for another window.
	reopenAt := opened.OpenUntil.Add(time.Minute)
	reopened := breaker(reopenAt, anyInput)
	assert.Equal(t, 3, requestCalls, "half-open: request is called")
	assert.True(t, reopened.IsOpen(), "failed probe reopens circuit")
	require.NotNil(t, reopened.OpenUntil, "reopened: has OpenUntil")
	assert.Equal(t, reopenAt.Add(30*time.Minute), *reopened.OpenUntil, "open window is reset")

	// A successful half-open probe closes the circuit.
	requestFails = false
	recovered := breaker(reopened.OpenUntil.Add(time.Minute), anyInput)
	assert.Equal(t, 4, requestCalls, "half-open: request is called")
	assert.True(t, recovered.IsClosed(), "successful probe closes circuit")
	assert.Nil(t, recovered.OpenUntil, "closed: no OpenUntil")
	require.NotNil(t, recovered.RequestResult, "closed: has response")
	assert.Equal(t, 1, recovered.RequestResult, "response == 1")

	// After recovery, a new failure sequence starts at zero.
	requestFails = true
	// First failure remains below threshold.
	firstFailureAgain := breaker(now, anyInput)
	assert.Equal(t, 5, requestCalls)
	assert.True(t, firstFailureAgain.IsClosed())

	// Second failure reaches threshold and opens circuit.
	openAgain := breaker(now, anyInput)
	assert.Equal(t, 6, requestCalls)
	assert.True(t, openAgain.IsOpen())
}

func TestIfSuccessResetsErrorCounting(t *testing.T) {
	doRequest := func(_ int) int { return 1 }
	requestFails := true
	anyInput := rand.IntN(1000)
	breaker, _ := resilience.CreateCircuitBreaker(
		10,
		doRequest,
		func(_ int) bool { return requestFails },
	)
	for range 10 {
		breaker(time.Now(), anyInput)
	}
	requestFails = false
	ok := breaker(time.Now(), anyInput)
	assert.True(t, ok.IsClosed())
	requestFails = true
	fail := breaker(time.Now(), anyInput)
	assert.True(t, fail.IsClosed())
}

/*
Start two requests while circuit is closed;
block both inside doRequest with channels.
Release failing request first so it opens circuit, then release successful request;
assert breaker remains open, because second request was authorized from stale state.
*/
func TestCheckConcurrencyProtectionA(t *testing.T) {
	// arrange
	var (
		now             = time.Now()
		sync            = make(chan bool, 2)
		requestReleases = make(map[int]chan bool)
		requestFails    = false
		breakerCh       = make(chan *resilience.CircuitBreakerInfo[int], 2)
	)
	requestReleases[1], requestReleases[2] = make(chan bool), make(chan bool)
	doRequest := func(id int) int {
		sync <- true
		fmt.Printf("%d: waiting release\n", id)
		<-requestReleases[id]
		fmt.Printf("%d: doing request\n", id)
		return rand.Int()
	}
	breaker, _ := resilience.CreateCircuitBreaker(
		0, doRequest, func(_ int) bool { return requestFails },
	)
	// act
	go func() { breakerCh <- breaker(now, 1) }()
	go func() { breakerCh <- breaker(now, 2) }()
	<-sync
	<-sync
	// this request will be the first and will fail, what opens the breaker
	requestFails = true
	requestReleases[1] <- true
	firstResult := <-breakerCh
	// this request will be the second, will succeed and should keep the breaker open
	requestFails = false
	requestReleases[2] <- true
	secondResult := <-breakerCh
	// assert
	assert.True(t, firstResult.IsOpen(), "first request open the breaker")
	assert.True(t, secondResult.IsOpen(), "second request should keep the breaker open")
	assert.False(t, secondResult.IsClosed(), "second request should not close the breaker")
}
