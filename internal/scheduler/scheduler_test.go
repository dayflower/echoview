package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
)

func TestNewStoreExposesEveryTargetBeforeInitialCollection(t *testing.T) {
	targets := testTargets()
	store := NewStore(targets)
	snapshot := store.Snapshot()
	if len(snapshot.Instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(snapshot.Instances))
	}
	for _, item := range snapshot.Instances {
		if item.Completed || item.Succeeded || !item.CollectedAt.IsZero() || item.Error != "" {
			t.Fatalf("initial snapshot item = %#v", item)
		}
	}
}

func TestTargetFromInstanceUsesResolvedInterval(t *testing.T) {
	target := TargetFromInstance(metricsconfig.Instance{IntervalSeconds: 45})
	if target.Interval != 45*time.Second {
		t.Fatalf("interval = %s, want 45s", target.Interval)
	}
}

func TestStoreReplacesWholeSnapshotWithoutReusingFailedValue(t *testing.T) {
	targets := testTargets()
	store := NewStore(targets)
	firstAt := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	store.replace(targets[0], firstAt, model.InstanceResult{EOJ: targets[0].Instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0x80", Value: "on", Status: model.PropertyOK}}}, nil)
	first := store.Snapshot()
	if !first.Instances[0].Succeeded || len(first.Instances[0].Result.GetProperties) != 1 || first.Instances[1].Completed {
		t.Fatalf("first snapshot = %#v", first)
	}

	failedAt := firstAt.Add(time.Second)
	store.replace(targets[0], failedAt, model.InstanceResult{}, errors.New("socket closed"))
	second := store.Snapshot()
	item := second.Instances[0]
	if item.Succeeded || item.Error != "socket closed" || !item.CollectedAt.Equal(failedAt) || len(item.Result.GetProperties) != 0 {
		t.Fatalf("failed snapshot must not retain the previous value: %#v", item)
	}
	if second.Instances[1].Completed {
		t.Fatalf("uncollected instance changed: %#v", second.Instances[1])
	}
	if !second.Instances[0].LastSucceededAt.Equal(firstAt) {
		t.Fatalf("last successful collection = %s, want %s", second.Instances[0].LastSucceededAt, firstAt)
	}
}

func TestSnapshotDoesNotExposeStoredResultForMutation(t *testing.T) {
	targets := testTargets()
	store := NewStore(targets)
	name := "original"
	store.replace(targets[0], time.Now(), model.InstanceResult{EOJ: targets[0].Instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0x80", NameJA: &name, Value: []any{"on"}}}}, nil)
	first := store.Snapshot()
	*first.Instances[0].Result.GetProperties[0].NameJA = "changed"
	first.Instances[0].Result.GetProperties[0].Value.([]any)[0] = "off"
	second := store.Snapshot()
	if got := *second.Instances[0].Result.GetProperties[0].NameJA; got != "original" {
		t.Fatalf("stored name = %q, want original", got)
	}
	if got := second.Instances[0].Result.GetProperties[0].Value.([]any)[0]; got != "on" {
		t.Fatalf("stored value = %#v, want on", got)
	}
}

func TestSchedulerUsesIndependentIntervalsAndContinuesAfterFailures(t *testing.T) {
	targets := testTargets()
	collector := &recordingCollector{failAddress: targets[0].Instance.Address}
	store := NewStore(targets)
	scheduler, err := New(Config{Targets: targets, Collector: collector, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()

	deadline := time.After(time.Second)
	for {
		failed, succeeded := collector.counts()
		if failed >= 3 && succeeded >= 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("collection counts = failures %d, successes %d", failed, succeeded)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}

	snapshot := store.Snapshot()
	if !snapshot.Instances[0].Completed || snapshot.Instances[0].Succeeded || snapshot.Instances[0].Error == "" {
		t.Fatalf("failed instance snapshot = %#v", snapshot.Instances[0])
	}
	if !snapshot.Instances[1].Completed || !snapshot.Instances[1].Succeeded || snapshot.Instances[1].CollectedAt.IsZero() {
		t.Fatalf("successful instance snapshot = %#v", snapshot.Instances[1])
	}
}

func TestStoreKeepsConcurrentTargetUpdates(t *testing.T) {
	targets := testTargets()
	store := NewStore(targets)
	var wait sync.WaitGroup
	for _, target := range targets {
		wait.Add(1)
		go func(target Target) {
			defer wait.Done()
			store.replace(target, time.Now(), model.InstanceResult{EOJ: target.Instance.EOJ}, nil)
		}(target)
	}
	wait.Wait()
	snapshot := store.Snapshot()
	for _, item := range snapshot.Instances {
		if !item.Completed || !item.Succeeded {
			t.Fatalf("lost concurrent update: %#v", snapshot)
		}
	}
}

func TestSchedulerNotifiesAfterEveryCompletedCollection(t *testing.T) {
	targets := testTargets()
	events := make(chan CollectionEvent, 4)
	scheduler, err := New(Config{
		Targets: targets, Collector: &recordingCollector{failAddress: targets[0].Instance.Address},
		Observers: []CollectionObserver{CollectionObserverFunc(func(event CollectionEvent) { events <- event })},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	seen := map[string]CollectionEvent{}
	for len(seen) < len(targets) {
		select {
		case event := <-events:
			seen[event.Instance.Address] = event
		case <-time.After(time.Second):
			t.Fatal("did not receive all initial collection events")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		event, ok := seen[target.Instance.Address.String()]
		if !ok || len(event.Snapshot.Instances) != len(targets) || event.Instance.CollectedAt.IsZero() {
			t.Fatalf("event = %#v", event)
		}
		if target.Instance.Address == targets[0].Instance.Address && event.Instance.Succeeded {
			t.Fatalf("failed collection reported as successful: %#v", event.Instance)
		}
	}
}

func TestSchedulerStopsWithoutFurtherNotifications(t *testing.T) {
	targets := testTargets()
	var mu sync.Mutex
	count := 0
	first := make(chan struct{}, 1)
	scheduler, err := New(Config{
		Targets:   targets[:1],
		Collector: &recordingCollector{},
		Observers: []CollectionObserver{CollectionObserverFunc(func(CollectionEvent) {
			mu.Lock()
			count++
			mu.Unlock()
			first <- struct{}{}
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("did not receive initial event")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := count
	mu.Unlock()
	time.Sleep(3 * targets[0].Interval)
	mu.Lock()
	after := count
	mu.Unlock()
	if after != before {
		t.Fatalf("notifications after stop = %d, want %d", after, before)
	}
}

func TestSchedulerRemovesPermanentlyDisabledTargetAndLogsIt(t *testing.T) {
	targets := testTargets()
	var logs []string
	runner, err := New(Config{
		Targets:   targets,
		Collector: disabledCollector{disabled: targets[0].Instance.Address},
		Infof: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.After(time.Second)
	for {
		if len(runner.Store().Snapshot().Instances) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("disabled target remained in snapshot")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "disabled 192.0.2.10/0x027D01") {
		t.Fatalf("info logs = %#v", logs)
	}
}

func testTargets() []Target {
	return []Target{
		{Instance: metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x02, 0x7D, 0x01}}, Interval: 5 * time.Millisecond},
		{Instance: metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.11"), EOJ: echonet.EOJ{0x02, 0x7D, 0x01}}, Interval: 13 * time.Millisecond},
	}
}

type recordingCollector struct {
	mu          sync.Mutex
	failAddress netip.Addr
	failed      int
	succeeded   int
}

type disabledCollector struct{ disabled netip.Addr }

func (c disabledCollector) Collect(_ context.Context, instance metricsconfig.Instance) (model.InstanceResult, error) {
	if instance.Address == c.disabled {
		return model.InstanceResult{}, permanentlyDisabledError{}
	}
	return model.InstanceResult{EOJ: instance.EOJ}, nil
}

type permanentlyDisabledError struct{}

func (permanentlyDisabledError) Error() string       { return "profile did not match" }
func (permanentlyDisabledError) DisableTarget() bool { return true }

func (c *recordingCollector) Collect(_ context.Context, instance metricsconfig.Instance) (model.InstanceResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if instance.Address == c.failAddress {
		c.failed++
		return model.InstanceResult{}, errors.New("temporary network failure")
	}
	c.succeeded++
	return model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0x80", Status: model.PropertyTimeout}}}, nil
}

func (c *recordingCollector) counts() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed, c.succeeded
}
