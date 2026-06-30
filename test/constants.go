package test

import "github.com/google/uuid"

// TODO: better to seed data and get ids?
const (
	IdSourceAccountAsString  string = "e4215def-6f52-4f3a-8cd7-23e261bad9e7"
	IdDestinyAccountAsString string = "597cb0af-0562-496b-9802-94dc5b0f082d"
)

func IdSourceAccountAsUuid() uuid.UUID {
	result, _ := uuid.Parse(IdSourceAccountAsString)
	return result
}

func IdDestinyAccountAsUuid() uuid.UUID {
	result, _ := uuid.Parse(IdDestinyAccountAsString)
	return result
}
