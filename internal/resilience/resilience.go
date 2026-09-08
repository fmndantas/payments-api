package resilience

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

type CircuitBreakerStatus string

const (
	open   CircuitBreakerStatus = "open"
	closed CircuitBreakerStatus = "closed"
)

type DoRequest[U any, T any] = func(U) T

type CircuitBreakerInfo[T any] struct {
	id            uuid.UUID
	status        CircuitBreakerStatus
	OpenUntil     *time.Time
	RequestResult T
}

func (info *CircuitBreakerInfo[T]) IsClosed() bool {
	return info.status == closed
}

func (info *CircuitBreakerInfo[T]) IsOpen() bool {
	return info.status == open
}

func (info *CircuitBreakerInfo[T]) IsHalfOpen(nowReference time.Time) bool {
	if info.OpenUntil == nil {
		return false
	}
	return info.status == open && nowReference.Compare(*info.OpenUntil) == 1
}

func (info *CircuitBreakerInfo[T]) CopyWithNewId() *CircuitBreakerInfo[T] {
	return &CircuitBreakerInfo[T]{
		id:            uuid.New(),
		status:        info.status,
		OpenUntil:     info.OpenUntil,
		RequestResult: info.RequestResult,
	}
}

func getNextOpenUntil(nowReference time.Time) *time.Time {
	foo := nowReference.Add(30 * time.Minute)
	return &foo
}

type CircuitBreakerHandler[U any, T comparable] = func(time.Time, U) *CircuitBreakerInfo[T]

func CreateCircuitBreaker[U any, T comparable](
	maximumNumberOfErrorsBeforeOpen int,
	doRequest DoRequest[U, T],
	checkRequestIsErrored func(T) bool,
) (CircuitBreakerHandler[U, T], func(time.Time) bool) {
	var (
		numberOfErrors = 0
		state          = &CircuitBreakerInfo[T]{id: uuid.New(), status: closed, OpenUntil: nil}
		zeroT          T
		mutex          = sync.RWMutex{}
	)

	isOpen := func(nowReference time.Time) bool {
		return state.IsOpen() && !state.IsHalfOpen(nowReference)
	}

	handler := func(nowReference time.Time, requestInput U) *CircuitBreakerInfo[T] {
		var (
			stateSnapshot          *CircuitBreakerInfo[T]
			nextState              *CircuitBreakerInfo[T]
			numberOfErrorsSnapshot int
			nextNumberOfErrors     int
		)

		mutex.RLock()
		stateSnapshot = &CircuitBreakerInfo[T]{
			id:            state.id,
			status:        state.status,
			OpenUntil:     state.OpenUntil,
			RequestResult: state.RequestResult,
		}
		numberOfErrorsSnapshot = numberOfErrors
		mutex.RUnlock()

		if stateSnapshot.IsClosed() || stateSnapshot.IsHalfOpen(nowReference) {
			var (
				requestResponse  = doRequest(requestInput)
				requestIsErrored = checkRequestIsErrored(requestResponse)
			)
			if requestIsErrored {
				nextNumberOfErrors = numberOfErrorsSnapshot + 1
			} else {
				nextNumberOfErrors = 0
			}
			if stateSnapshot.IsClosed() && nextNumberOfErrors > maximumNumberOfErrorsBeforeOpen {
				nextState = &CircuitBreakerInfo[T]{
					id:            uuid.New(),
					status:        open,
					OpenUntil:     getNextOpenUntil(nowReference),
					RequestResult: zeroT,
				}
			} else if stateSnapshot.IsHalfOpen(nowReference) && requestIsErrored {
				nextNumberOfErrors = 0
				nextState = &CircuitBreakerInfo[T]{
					id:            uuid.New(),
					status:        open,
					OpenUntil:     getNextOpenUntil(nowReference),
					RequestResult: zeroT,
				}
			} else {
				nextState = &CircuitBreakerInfo[T]{
					id:            uuid.New(),
					status:        closed,
					OpenUntil:     nil,
					RequestResult: requestResponse,
				}
			}
		} else {
			nextState = stateSnapshot.CopyWithNewId()
		}

		mutex.Lock()
		defer mutex.Unlock()

		if stateSnapshot.id == state.id {
			state = nextState
			numberOfErrors = nextNumberOfErrors
		}

		return state
	}

	return handler, isOpen
}
