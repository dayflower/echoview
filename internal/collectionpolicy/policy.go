// Package collectionpolicy resolves polling settings from their configuration layers.
package collectionpolicy

import "fmt"

const (
	// FallbackIntervalSeconds is used when no configuration layer sets an interval.
	FallbackIntervalSeconds = 60
	// FallbackBatchSize requests all compatible EPCs in one GET.
	FallbackBatchSize = -1
)

// Policy is an optional collection-policy layer. Nil fields inherit from a
// less-specific layer.
type Policy struct {
	IntervalSeconds *int
	BatchSize       *int
}

// ResolvedPolicy is the complete policy used by a collector.
type ResolvedPolicy struct {
	IntervalSeconds int
	BatchSize       int
}

// Validate checks values that are present in one policy layer.
func (p Policy) Validate() error {
	if p.IntervalSeconds != nil && *p.IntervalSeconds <= 0 {
		return fmt.Errorf("interval_seconds must be greater than zero")
	}
	if p.BatchSize != nil && *p.BatchSize == 0 {
		return fmt.Errorf("batch_size must not be zero")
	}
	return nil
}

// Resolve applies policy layers from least to most specific.
func Resolve(layers ...Policy) (ResolvedPolicy, error) {
	result := ResolvedPolicy{IntervalSeconds: FallbackIntervalSeconds, BatchSize: FallbackBatchSize}
	for _, layer := range layers {
		if err := layer.Validate(); err != nil {
			return ResolvedPolicy{}, err
		}
		if layer.IntervalSeconds != nil {
			result.IntervalSeconds = *layer.IntervalSeconds
		}
		if layer.BatchSize != nil {
			result.BatchSize = *layer.BatchSize
		}
	}
	return result, nil
}
