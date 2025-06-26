package handler

// Result after dispatching a request from a processor to the backend.
type DispatchResult struct {
	Code     int
	Duration float64
	Body     []byte
	Tenant   string
	Error    error
}
