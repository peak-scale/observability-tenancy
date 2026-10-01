package handler

// DispatchResult describes the result of forwarding a request to the backend.
type DispatchResult struct {
	Code     int
	Duration float64
	Body     []byte
	Tenant   string
	Error    error
}
