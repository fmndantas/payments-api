package eventstatus

type OutboxStatus string

var (
	Unprocessed = OutboxStatus("unprocessed")
	Retry       = OutboxStatus("retry")
	Errored     = OutboxStatus("errored")
	Success     = OutboxStatus("success")
)

func (s OutboxStatus) String() string { return string(s) }
