package resilience

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

type circuitBreakerStatus string

const (
	Open   circuitBreakerStatus = "open"
	Closed circuitBreakerStatus = "closed"
)

type DoRequest[U any, T any] = func(context.Context, U) T

type CircuitBreakerState[T any] struct {
	id             uuid.UUID
	status         circuitBreakerStatus
	numberOfErrors int
	openUntil      *time.Time
	requestResult  T
}

func CreateState[T any](
	status circuitBreakerStatus,
	numberOfErrors int,
	OpenUntil *time.Time,
	RequestResult T,
) *CircuitBreakerState[T] {
	return &CircuitBreakerState[T]{
		id:             uuid.New(),
		status:         status,
		numberOfErrors: numberOfErrors,
		openUntil:      OpenUntil,
		requestResult:  RequestResult,
	}
}

func (state CircuitBreakerState[T]) IsClosed() bool {
	return state.status == Closed
}

func (state CircuitBreakerState[T]) IsOpen() bool {
	return state.status == Open
}

func (state CircuitBreakerState[T]) shouldProbe(nowReference time.Time) bool {
	if state.openUntil == nil {
		return false
	}
	return state.status == Open && nowReference.Compare(*state.openUntil) >= 0
}

func (state CircuitBreakerState[T]) Status() circuitBreakerStatus {
	return state.status
}

func (state CircuitBreakerState[T]) OpenUntil() *time.Time {
	return state.openUntil
}

func (state CircuitBreakerState[T]) RequestResult() T {
	return state.requestResult
}

func (state CircuitBreakerState[T]) NumberOfErrors() int {
	return state.numberOfErrors
}

func (state CircuitBreakerState[T]) Repeat() *CircuitBreakerState[T] {
	return &CircuitBreakerState[T]{
		id:             uuid.New(),
		status:         state.status,
		numberOfErrors: state.numberOfErrors,
		openUntil:      state.openUntil,
		requestResult:  state.requestResult,
	}
}

func (state CircuitBreakerState[T]) ConstructNextState(
	nowReference time.Time,
	requestResult T,
	maximumNumberOfErrorsBeforeOpen int,
	isRequestResultErroredFn func(T) bool,
) *CircuitBreakerState[T] {
	var (
		isRequestResultErrored = isRequestResultErroredFn(requestResult)
		openUntil              = nowReference.Add(30 * time.Minute)
	)

	switch {
	case state.IsClosed():
		if isRequestResultErrored {
			if state.numberOfErrors >= maximumNumberOfErrorsBeforeOpen {
				return CreateState(Open, state.numberOfErrors+1, &openUntil, requestResult)
			} else {
				return CreateState(Closed, state.numberOfErrors+1, nil, requestResult)
			}
		} else {
			return CreateState(Closed, 0, nil, requestResult)
		}
	case state.shouldProbe(nowReference):
		if isRequestResultErrored {
			return CreateState(Open, state.numberOfErrors+1, &openUntil, requestResult)
		} else {
			return CreateState(Closed, 0, nil, requestResult)
		}
	case state.IsOpen():
		return state.Repeat()
	default:
		return state.Repeat()
	}
}

type CircuitBreakerHandler[U any, T comparable] = func(context.Context, time.Time, U) *CircuitBreakerState[T]

func CreateCircuitBreaker[U any, T comparable](
	maximumNumberOfErrorsBeforeOpen int,
	doRequest DoRequest[U, T],
	checkRequestIsErrored func(context.Context, T) bool,
) (CircuitBreakerHandler[U, T], func(time.Time) bool) {
	var (
		mutex = sync.RWMutex{}
		zeroT T
		state = CreateState(Closed, 0, nil, zeroT)
	)

	isOpen := func(nowReference time.Time) bool {
		mutex.RLock()
		defer mutex.RUnlock()

		return state.IsOpen() && !state.shouldProbe(nowReference)
	}

	handler := func(ctx context.Context, nowReference time.Time, requestInput U) *CircuitBreakerState[T] {
		var (
			requestResult                T
			checkRequestIsErroredWithCtx = func(input T) bool {
				return checkRequestIsErrored(ctx, input)
			}
		)

		mutex.Lock()
		defer mutex.Unlock()

		if state.IsClosed() {
			requestResult = doRequest(ctx, requestInput)
		}

		if state.shouldProbe(nowReference) {
			requestResult = doRequest(ctx, requestInput)
		}

		state = state.ConstructNextState(
			nowReference, requestResult, maximumNumberOfErrorsBeforeOpen, checkRequestIsErroredWithCtx,
		)

		return state
	}

	return handler, isOpen
}
