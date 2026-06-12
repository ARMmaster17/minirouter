package app

// ActiveRequestCounter tracks in-flight provider requests.
type ActiveRequestCounter interface {
	Increment(providerID string) int
	Decrement(providerID string) int
	Count(providerID string) int
}
