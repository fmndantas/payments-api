package resilience

import (
	"time"
)

type CircuitBreakerStatus string

const (
	open   CircuitBreakerStatus = "open"
	closed CircuitBreakerStatus = "closed"
)

type DoRequest[U any, T any] = func(U) T

type CircuitBreakerInfo[T any] struct {
	status        CircuitBreakerStatus
	OpenUntil     *time.Time
	RequestResult *T
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

func getNextOpenUntil(nowReference time.Time) *time.Time {
	foo := nowReference.Add(30 * time.Minute)
	return &foo
}

type CircuitBreakerHandler[U any, T any] = func(time.Time, U) *CircuitBreakerInfo[T]

func CreateCircuitBreaker[U any, T any](
	maximumNumberOfErrorsBeforeOpen int,
	doRequest DoRequest[U, T],
	checkRequestIsErrored func(T) bool,
) (CircuitBreakerHandler[U, T], func(time.Time) bool) {
	var (
		currentNumberOfErrors = 0
		currentInfo           = &CircuitBreakerInfo[T]{status: closed, OpenUntil: nil}
	)

	isOpen := func(nowReference time.Time) bool {
		return currentInfo.IsOpen() && !currentInfo.IsHalfOpen(nowReference)
	}

	handler := func(nowReference time.Time, requestInput U) *CircuitBreakerInfo[T] {
		if currentInfo.IsClosed() || currentInfo.IsHalfOpen(nowReference) {
			requestResponse := doRequest(requestInput)
			requestIsErrored := checkRequestIsErrored(requestResponse)
			if requestIsErrored {
				currentNumberOfErrors += 1
			}
			if currentInfo.IsClosed() && currentNumberOfErrors > maximumNumberOfErrorsBeforeOpen {
				currentInfo = &CircuitBreakerInfo[T]{
					status:        open,
					OpenUntil:     getNextOpenUntil(nowReference),
					RequestResult: nil,
				}
			} else if currentInfo.IsHalfOpen(nowReference) && requestIsErrored {
				currentNumberOfErrors = 0
				currentInfo = &CircuitBreakerInfo[T]{
					status:        open,
					OpenUntil:     getNextOpenUntil(nowReference),
					RequestResult: nil,
				}
			} else {
				currentInfo = &CircuitBreakerInfo[T]{
					status:        closed,
					OpenUntil:     nil,
					RequestResult: &requestResponse,
				}
			}
		}
		return currentInfo
	}

	return handler, isOpen
}
