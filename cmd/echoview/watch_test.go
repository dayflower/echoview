package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/scheduler"
)

func TestWatchPrinterWritesTextForSuccessfulAndFailedCollections(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "watch-output")
	if err != nil {
		t.Fatal(err)
	}
	printer := &watchPrinter{output: output, format: "text"}
	collectedAt := time.Date(2026, 9, 16, 12, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	raw := "0x2A"
	printer.ObserveCollection(scheduler.CollectionEvent{Instance: scheduler.InstanceSnapshot{
		Address: "192.0.2.10", EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, CollectedAt: collectedAt, Succeeded: true,
		Result: model.InstanceResult{EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, GetProperties: []model.PropertyResult{{EPC: "0xE0", RawEDT: &raw, Value: "42", Status: model.PropertyOK}, {EPC: "0xE1", RawEDT: &raw, Value: "ON", Status: model.PropertySNA}}},
	}})
	printer.ObserveCollection(scheduler.CollectionEvent{Instance: scheduler.InstanceSnapshot{
		Address: "192.0.2.11", EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, CollectedAt: collectedAt, Error: "socket closed",
	}})
	if err := printer.Err(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{"2026-09-16T12:00:00+09:00 192.0.2.10 / 0x027D01", "0xE0): 42", "0xE1): ON (sna)", "collection: failed (socket closed)"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("watch text does not contain %q:\n%s", expected, text)
		}
	}
}

func TestWatchPrinterWritesJSONLines(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "watch-json")
	if err != nil {
		t.Fatal(err)
	}
	printer := &watchPrinter{output: output, format: "jsonl"}
	collectedAt := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	printer.ObserveCollection(scheduler.CollectionEvent{Instance: scheduler.InstanceSnapshot{
		Address: "192.0.2.10", EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, CollectedAt: collectedAt, Succeeded: true,
		Result: model.InstanceResult{EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, GetProperties: []model.PropertyResult{{EPC: "0xE0", Status: model.PropertyTimeout}}},
	}})
	if err := printer.Err(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var event map[string]any
	if err := json.NewDecoder(input).Decode(&event); err != nil {
		t.Fatal(err)
	}
	result, ok := event["result"].(map[string]any)
	if !ok {
		t.Fatalf("JSON Lines result = %#v", event["result"])
	}
	properties, ok := result["get_properties"].([]any)
	if !ok || len(properties) != 1 {
		t.Fatalf("JSON Lines properties = %#v", result["get_properties"])
	}
	property := properties[0].(map[string]any)
	if event["collected_at"] != collectedAt.Format(time.RFC3339) || event["address"] != "192.0.2.10" || event["eoj"] != "0x027D01" || event["succeeded"] != true || property["value"] != nil || property["status"] != string(model.PropertyTimeout) {
		t.Fatalf("JSON Lines event = %#v", event)
	}
}

func TestWatchUsageDocumentsJSONLines(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "watch-usage")
	if err != nil {
		t.Fatal(err)
	}
	printWatchUsage(output)
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "--format text|jsonl") || !strings.Contains(string(content), "--keep-binding") {
		t.Fatalf("watch usage = %s", content)
	}
}
