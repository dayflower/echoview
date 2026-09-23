// Package scheduler periodically collects configured ECHONET Lite instances.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
)

// Collector obtains one configured instance. Implementations must honor ctx.
type Collector interface {
	Collect(context.Context, metricsconfig.Instance) (model.InstanceResult, error)
}

// Target associates an instance with its collection interval.
type Target struct {
	Instance metricsconfig.Instance
	Interval time.Duration
}

// TargetFromInstance converts the interval in a format-2 configuration into a
// scheduler target.
func TargetFromInstance(instance metricsconfig.Instance) Target {
	return Target{Instance: instance, Interval: time.Duration(instance.IntervalSeconds) * time.Second}
}

// InstanceSnapshot is the latest completed collection attempt for one target.
// Result is empty when collection failed or has not yet run. LastSucceededAt
// retains only the time of the last successful collection and never retains a
// previous result. Property-level failures are represented in
// Result.GetProperties rather than as an Error.
type InstanceSnapshot struct {
	Address         string
	EOJ             echonet.EOJ
	CollectedAt     time.Time
	LastSucceededAt time.Time
	Completed       bool
	Succeeded       bool
	Result          model.InstanceResult
	Error           string
}

// Snapshot is an immutable, complete view of every configured target.
type Snapshot struct {
	Instances []InstanceSnapshot
}

// SnapshotReader is the common read-only boundary for snapshot consumers such
// as Prometheus and MQTT publishers.
type SnapshotReader interface {
	Snapshot() Snapshot
}

// CollectionEvent is emitted after a target's collection result has been
// atomically incorporated into Snapshot.
type CollectionEvent struct {
	Snapshot Snapshot
	Instance InstanceSnapshot
}

// CollectionObserver receives completed collection events. Implementations
// should return promptly so that a target's next collection is not delayed.
type CollectionObserver interface {
	ObserveCollection(CollectionEvent)
}

// CollectionObserverFunc adapts a function to CollectionObserver.
type CollectionObserverFunc func(CollectionEvent)

// ObserveCollection calls f with event.
func (f CollectionObserverFunc) ObserveCollection(event CollectionEvent) { f(event) }

// Store atomically publishes complete snapshots.
type Store struct {
	value atomic.Pointer[Snapshot]
}

// NewStore returns a store whose initial snapshot has one incomplete item per
// configured target.
func NewStore(targets []Target) *Store {
	items := make([]InstanceSnapshot, len(targets))
	for i, target := range targets {
		items[i] = InstanceSnapshot{Address: target.Instance.Address.String(), EOJ: target.Instance.EOJ}
	}
	store := &Store{}
	store.value.Store(&Snapshot{Instances: items})
	return store
}

// Snapshot returns a defensive copy of the last atomically published state.
func (s *Store) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	value := s.value.Load()
	if value == nil {
		return Snapshot{}
	}
	return cloneSnapshot(*value)
}

func (s *Store) replace(target Target, collectedAt time.Time, result model.InstanceResult, err error) Snapshot {
	for {
		previous := s.value.Load()
		next := cloneSnapshot(*previous)
		for i := range next.Instances {
			item := &next.Instances[i]
			if item.Address != target.Instance.Address.String() || item.EOJ != target.Instance.EOJ {
				continue
			}
			item.CollectedAt = collectedAt
			item.Completed = true
			item.Succeeded = err == nil
			item.Error = ""
			item.Result = model.InstanceResult{}
			if err != nil {
				item.Error = err.Error()
			} else {
				item.LastSucceededAt = collectedAt
				item.Result = cloneInstanceResult(result)
			}
			break
		}
		if s.value.CompareAndSwap(previous, &next) {
			return next
		}
	}
}

// remove permanently excludes one target from subsequent snapshots.
func (s *Store) remove(target Target) Snapshot {
	for {
		previous := s.value.Load()
		next := Snapshot{Instances: make([]InstanceSnapshot, 0, len(previous.Instances))}
		for _, item := range previous.Instances {
			if item.Address == target.Instance.Address.String() && item.EOJ == target.Instance.EOJ {
				continue
			}
			next.Instances = append(next.Instances, cloneInstanceSnapshot(item))
		}
		if s.value.CompareAndSwap(previous, &next) {
			return next
		}
	}
}

// Config controls a scheduler run.
type Config struct {
	Targets   []Target
	Collector Collector
	Store     *Store
	Now       func() time.Time
	Infof     func(string, ...any)
	Observers []CollectionObserver
}

// Scheduler performs an initial collection for every target, then repeats each
// target independently at its own interval.
type Scheduler struct {
	targets   []Target
	collector Collector
	store     *Store
	now       func() time.Time
	infof     func(string, ...any)
	observers []CollectionObserver
}

// New validates and constructs a scheduler. The provided Store is updated in
// place; omit it to create a new store.
func New(config Config) (*Scheduler, error) {
	if config.Collector == nil {
		return nil, errors.New("scheduler has no collector")
	}
	targets := append([]Target(nil), config.Targets...)
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.Interval <= 0 {
			return nil, fmt.Errorf("collection interval for %s/%s must be greater than zero", target.Instance.Address, target.Instance.EOJ)
		}
		key := target.Instance.Address.String() + "/" + target.Instance.EOJ.String()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate scheduler target %s", key)
		}
		seen[key] = struct{}{}
	}
	store := config.Store
	if store == nil {
		store = NewStore(targets)
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	observers := make([]CollectionObserver, 0, len(config.Observers))
	for _, observer := range config.Observers {
		if observer != nil {
			observers = append(observers, observer)
		}
	}
	return &Scheduler{targets: targets, collector: config.Collector, store: store, now: now, infof: config.Infof, observers: observers}, nil
}

// Store exposes the scheduler's read-only snapshot source.
func (s *Scheduler) Store() SnapshotReader { return s.store }

// Run blocks until ctx is canceled. A failed collection only updates that
// target's snapshot and never stops the workers for other targets.
func (s *Scheduler) Run(ctx context.Context) error {
	done := make(chan struct{}, len(s.targets))
	for _, target := range s.targets {
		go func(target Target) {
			defer func() { done <- struct{}{} }()
			s.runTarget(ctx, target)
		}(target)
	}
	for range s.targets {
		<-done
	}
	return nil
}

func (s *Scheduler) runTarget(ctx context.Context, target Target) {
	for {
		result, err := s.collector.Collect(ctx, target.Instance)
		if ctx.Err() != nil {
			return
		}
		if disablesTarget(err) {
			s.store.remove(target)
			if s.infof != nil {
				s.infof("disabled %s/%s: %v", target.Instance.Address, target.Instance.EOJ, err)
			}
			return
		}
		snapshot := s.store.replace(target, s.now(), result, err)
		s.notify(snapshot, target)
		timer := time.NewTimer(target.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type targetDisablingError interface {
	DisableTarget() bool
}

func disablesTarget(err error) bool {
	var disabling targetDisablingError
	return errors.As(err, &disabling) && disabling.DisableTarget()
}

func (s *Scheduler) notify(snapshot Snapshot, target Target) {
	for _, item := range snapshot.Instances {
		if item.Address != target.Instance.Address.String() || item.EOJ != target.Instance.EOJ {
			continue
		}
		for _, observer := range s.observers {
			observer.ObserveCollection(CollectionEvent{Snapshot: cloneSnapshot(snapshot), Instance: cloneInstanceSnapshot(item)})
		}
		return
	}
}

func cloneSnapshot(source Snapshot) Snapshot {
	result := Snapshot{Instances: make([]InstanceSnapshot, len(source.Instances))}
	for i, item := range source.Instances {
		result.Instances[i] = cloneInstanceSnapshot(item)
	}
	return result
}

func cloneInstanceSnapshot(source InstanceSnapshot) InstanceSnapshot {
	result := source
	result.Result = cloneInstanceResult(source.Result)
	return result
}

func cloneInstanceResult(source model.InstanceResult) model.InstanceResult {
	result := source
	result.ClassName = cloneString(source.ClassName)
	result.ClassNameEN = cloneString(source.ClassNameEN)
	result.ClassShortName = cloneString(source.ClassShortName)
	result.GetProperties = make([]model.PropertyResult, len(source.GetProperties))
	for i, property := range source.GetProperties {
		result.GetProperties[i] = property
		result.GetProperties[i].NameJA = cloneString(property.NameJA)
		result.GetProperties[i].NameEN = cloneString(property.NameEN)
		result.GetProperties[i].ShortName = cloneString(property.ShortName)
		result.GetProperties[i].RawEDT = cloneString(property.RawEDT)
		result.GetProperties[i].ValueNameJA = cloneString(property.ValueNameJA)
		result.GetProperties[i].Unit = cloneString(property.Unit)
		result.GetProperties[i].Value = cloneValue(property.Value)
	}
	if source.Profile != nil {
		profile := *source.Profile
		profile.NameJA = cloneString(source.Profile.NameJA)
		profile.NameEN = cloneString(source.Profile.NameEN)
		result.Profile = &profile
	}
	return result
}

func cloneString(source *string) *string {
	if source == nil {
		return nil
	}
	result := *source
	return &result
}

func cloneValue(source any) any {
	switch value := source.(type) {
	case []any:
		result := make([]any, len(value))
		for i, item := range value {
			result[i] = cloneValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			result[key] = cloneValue(item)
		}
		return result
	default:
		return source
	}
}
