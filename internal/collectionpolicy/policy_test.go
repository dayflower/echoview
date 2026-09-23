package collectionpolicy

import "testing"

func TestResolveAppliesLayersInOrder(t *testing.T) {
	interval120, interval30, batch8, batch2 := 120, 30, 8, 2
	resolved, err := Resolve(
		Policy{IntervalSeconds: &interval120},
		Policy{BatchSize: &batch8},
		Policy{IntervalSeconds: &interval30},
		Policy{BatchSize: &batch2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.IntervalSeconds != 30 || resolved.BatchSize != 2 {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestResolveUsesFallbacksAndRejectsInvalidValues(t *testing.T) {
	resolved, err := Resolve()
	if err != nil || resolved.IntervalSeconds != 60 || resolved.BatchSize != -1 {
		t.Fatalf("Resolve() = %#v, %v", resolved, err)
	}
	zero := 0
	if _, err := Resolve(Policy{BatchSize: &zero}); err == nil {
		t.Fatal("Resolve accepted batch_size: 0")
	}
	if _, err := Resolve(Policy{IntervalSeconds: &zero}); err == nil {
		t.Fatal("Resolve accepted interval_seconds: 0")
	}
}
