package message

// Prices are the credits charged per segment for each service class.
type Prices struct {
	Normal  int64
	Express int64
}

// PerSegment returns the price of one segment of class t.
func (p Prices) PerSegment(t Type) int64 {
	if t == Express {
		return p.Express
	}
	return p.Normal
}

// Cost returns the credits charged for a message of class t.
func (p Prices) Cost(t Type, segments int) int64 {
	return int64(segments) * p.PerSegment(t)
}
