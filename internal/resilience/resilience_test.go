package resilience_test

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	sut "github.com/fmndantas/payments/internal/resilience"
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

	breaker, _ := sut.CreateCircuitBreaker(
		1,
		doRequest,
		func(_ int) bool { return requestFails },
	)

	// First failed request stays below the threshold.
	firstFailure := breaker(now, anyInput)
	assert.Equal(t, 1, requestCalls)
	assert.True(t, firstFailure.IsClosed(), "should remain closed")
	assert.Nil(t, firstFailure.OpenUntil(), "closed: no OpenUntil")
	require.NotNil(t, firstFailure.RequestResult(), "closed: has response")
	assert.Equal(t, 1, firstFailure.RequestResult(), "response == 1")

	// Second failed request reaches the threshold and opens the circuit.
	opened := breaker(now, anyInput)
	assert.Equal(t, 2, requestCalls)
	assert.True(t, opened.IsOpen(), "should open at threshold")
	require.NotNil(t, opened.OpenUntil(), "open: has OpenUntil")

	// Requests before OpenUntil are short-circuited.
	stillOpenAt := opened.OpenUntil().Add(-time.Minute)
	stillOpen := breaker(stillOpenAt, anyInput)
	assert.Equal(t, 2, requestCalls, "open: request is not called")
	assert.True(t, stillOpen.IsOpen(), "should stay open")
	assert.Equal(t, opened.OpenUntil(), stillOpen.OpenUntil(), "open window is unchanged")

	// A failed half-open probe reopens the circuit for another window.
	reopenAt := opened.OpenUntil().Add(time.Minute)
	reopened := breaker(reopenAt, anyInput)
	assert.Equal(t, 3, requestCalls, "half-open: request is called")
	assert.True(t, reopened.IsOpen(), "failed probe reopens circuit")
	require.NotNil(t, reopened.OpenUntil(), "reopened: has OpenUntil")
	assert.Equal(t, reopenAt.Add(30*time.Minute), *reopened.OpenUntil(), "open window is reset")

	// A successful half-open probe closes the circuit.
	requestFails = false
	recovered := breaker(reopened.OpenUntil().Add(time.Minute), anyInput)
	assert.Equal(t, 4, requestCalls, "half-open: request is called")
	assert.True(t, recovered.IsClosed(), "successful probe closes circuit")
	assert.Nil(t, recovered.OpenUntil(), "closed: no OpenUntil")
	require.NotNil(t, recovered.RequestResult(), "closed: has response")
	assert.Equal(t, 1, recovered.RequestResult(), "response == 1")

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

/*
SUT is a circuit breaker that opens at the eleventh error.
Trigger 10 failling requests.
Then, the eleventh request succeeds.
Then, a failling request should keep the breaker closed
because the number of errors returned to zero in the last request.
*/
func TestIfSuccessResetsErrorCounting(t *testing.T) {
	doRequest := func(_ int) int { return 1 }
	requestFails := true
	anyInput := rand.IntN(1000)
	breaker, _ := sut.CreateCircuitBreaker(
		10,
		doRequest,
		func(_ int) bool { return requestFails },
	)
	for range 10 {
		breaker(time.Now(), anyInput)
	}
	requestFails = false
	ok := breaker(time.Now(), anyInput)
	assert.True(t, ok.IsClosed(), "ok is closed")
	requestFails = true
	fail := breaker(time.Now(), anyInput)
	assert.True(t, fail.IsClosed(), "fail is closed")
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
		syncCh          = make(chan struct{}, 2)
		requestReleases = make(map[int]chan struct{})
		requestFails    = false
		breakerCh       = make(chan *sut.CircuitBreakerState[int], 2)
	)
	requestReleases[1], requestReleases[2] = make(chan struct{}), make(chan struct{})
	doRequest := func(id int) int {
		syncCh <- struct{}{}
		fmt.Printf("%d: waiting release\n", id)
		<-requestReleases[id]
		fmt.Printf("%d: doing request\n", id)
		return rand.Int()
	}
	breaker, _ := sut.CreateCircuitBreaker(
		0, doRequest, func(_ int) bool { return requestFails },
	)
	// act
	go func() { breakerCh <- breaker(now, 1) }()
	go func() { breakerCh <- breaker(now, 2) }()
	<-syncCh
	<-syncCh
	// this request will be the first and will fail, what opens the breaker
	requestFails = true
	requestReleases[1] <- struct{}{}
	firstResult := <-breakerCh
	// this request will be the second, will succeed and should keep the breaker open
	requestFails = false
	requestReleases[2] <- struct{}{}
	secondResult := <-breakerCh
	// assert
	assert.True(t, firstResult.IsOpen(), "first request open the breaker")
	assert.True(t, secondResult.IsOpen(), "second request should keep the breaker open")
	assert.False(t, secondResult.IsClosed(), "second request should not close the breaker")
}

/*
Trigger N failling, concurrent requests
and assert that a N-1 circuit breaker opens at the N request
*/
func TestCheckConcurrencyProtectionB(t *testing.T) {
	var (
		now       = time.Now()
		N         = 10 // number of requests
		syncCh    = make(chan struct{})
		breakerCh = make(chan *sut.CircuitBreakerState[int], N)
		wg        = sync.WaitGroup{}
	)
	doRequest := func(id int) int {
		<-syncCh
		fmt.Printf("%d: doing request\n", id)
		return rand.Int()
	}
	// creates a breaker that fails at the N request
	breaker, _ := sut.CreateCircuitBreaker(
		N-1, doRequest, func(_ int) bool { return true },
	)
	// run N failling requests
	for i := range N {
		wg.Go(func() { breakerCh <- breaker(now, i+1) })
	}
	// closes syncCh so that all request are released at the same time
	close(syncCh)
	wg.Wait()
	// assert
	var (
		results = make([]*sut.CircuitBreakerState[int], N)
		open    *sut.CircuitBreakerState[int]
	)
	assert.Len(t, results, N)
	for i := range N {
		results[i] = <-breakerCh
		if results[i].IsOpen() {
			open = results[i]
		}
	}
	require.NotNil(t, open, "open")
	assert.Equal(t, 10, open.NumberOfErrors(), "open.NumberOfErrors")
}

/*
Test different transitions between circuit breaker states
*/
func TestConstructNextState(t *testing.T) {
	type Foo struct{}
	var (
		tsCall        = time.Now()
		tsCallPlus30m = tsCall.Add(30 * time.Minute)
	)
	cases := []struct {
		idCase           string
		state            *sut.CircuitBreakerState[Foo]
		requestIsErrored bool
		expected         *sut.CircuitBreakerState[Foo]
	}{
		{
			"close -> close (ok)",
			sut.CreateState(sut.Closed, 0, nil, Foo{}),
			false,
			sut.CreateState(sut.Closed, 0, nil, Foo{}),
		},
		{
			"close -> close (error)",
			sut.CreateState(sut.Closed, 0, nil, Foo{}),
			true,
			sut.CreateState(sut.Closed, 1, nil, Foo{}),
		},
		{
			"close -> open",
			sut.CreateState(sut.Closed, 5, nil, Foo{}),
			true,
			sut.CreateState(sut.Open, 6, &tsCallPlus30m, Foo{}),
		},
		{
			/*
				don't enter half-open because the circuit will be open until tsCall + 30m
				and the function will be called at tsCall
			*/
			"open -> open",
			sut.CreateState(sut.Open, 6, &tsCallPlus30m, Foo{}),
			true,
			sut.CreateState(sut.Open, 6, &tsCallPlus30m, Foo{}),
		},
		{
			"open -> errored half-open -> open",
			sut.CreateState(sut.Open, 6, &tsCall, Foo{}),
			true,
			sut.CreateState(sut.Open, 7, &tsCallPlus30m, Foo{}),
		},
		{
			"open -> successful half-open -> closed",
			sut.CreateState(sut.Open, 6, &tsCall, Foo{}),
			false,
			sut.CreateState(sut.Closed, 0, nil, Foo{}),
		},
	}
	for _, tt := range cases {
		t.Run(tt.idCase, func(t *testing.T) {
			result := tt.state.ConstructNextState(tsCall, Foo{}, 5, func(_ Foo) bool { return tt.requestIsErrored })
			assert.Equal(t, tt.expected.Status(), result.Status(), "status")
			assert.Equal(t, tt.expected.OpenUntil(), result.OpenUntil(), "open until")
			assert.Equal(t, tt.expected.NumberOfErrors(), result.NumberOfErrors(), "number of errors")
		})
	}
}
