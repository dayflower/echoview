package mqtt

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/scheduler"
)

func TestPublisherPublishesRetainedValuesUnavailableValuesAndReconnectSnapshot(t *testing.T) {
	broker := newTestBroker(t, true)
	defer broker.close()
	instance := metricsconfig.Instance{Address: netip.MustParseAddr("192.0.2.10"), EOJ: echonet.EOJ{0x02, 0x7D, 0x01}, MetricPrefix: "echonet_battery_", Properties: []metricsconfig.Property{{EPC: 0xE0}}}
	publisher, err := New(Config{BrokerURL: "mqtt://" + broker.address(), TopicPrefix: "home", ClientID: "test-client", QoS: 1, Retain: true, ReconnectMin: time.Millisecond, ReconnectMax: 5 * time.Millisecond, Instances: []metricsconfig.Instance{instance}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- publisher.Run(ctx) }()
	raw, unit := "0x002A", "kWh"
	at := time.Date(2026, 9, 17, 1, 2, 3, 0, time.UTC)
	publisher.ObserveCollection(scheduler.CollectionEvent{Snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{{Address: instance.Address.String(), EOJ: instance.EOJ, CollectedAt: at, Completed: true, Succeeded: true, Result: model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0xE0", RawEDT: &raw, Value: 42, Unit: &unit, Status: model.PropertyOK}}}}}}, Instance: scheduler.InstanceSnapshot{Address: instance.Address.String(), EOJ: instance.EOJ, CollectedAt: at, Completed: true, Succeeded: true, Result: model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0xE0", RawEDT: &raw, Value: 42, Unit: &unit, Status: model.PropertyOK}}}}})
	first := broker.waitProperty(t, 2*time.Second)
	if first.topic != "home/echonet_battery_/0x027D01/0xE0" || !first.retain {
		t.Fatalf("first MQTT publish = %#v", first)
	}
	var firstPayload Payload
	if err := json.Unmarshal(first.payload, &firstPayload); err != nil {
		t.Fatal(err)
	}
	if firstPayload.Value.(float64) != 42 || firstPayload.RawEDT == nil || *firstPayload.RawEDT != raw || firstPayload.Unit == nil || *firstPayload.Unit != unit || !firstPayload.CollectedAt.Equal(at) {
		t.Fatalf("first payload = %#v", firstPayload)
	}

	// The broker closes the first connection after the first property. A later
	// failed collection therefore exercises reconnect and republishes only the
	// latest snapshot, with null replacing the old retained value.
	failureAt := at.Add(time.Minute)
	failure := scheduler.InstanceSnapshot{Address: instance.Address.String(), EOJ: instance.EOJ, CollectedAt: failureAt, Completed: true, Succeeded: true, Result: model.InstanceResult{EOJ: instance.EOJ, GetProperties: []model.PropertyResult{{EPC: "0xE0", RawEDT: &raw, Value: 42, Unit: &unit, Status: model.PropertyTimeout}}}}
	publisher.ObserveCollection(scheduler.CollectionEvent{Snapshot: scheduler.Snapshot{Instances: []scheduler.InstanceSnapshot{failure}}, Instance: failure})
	second := broker.waitProperty(t, 2*time.Second)
	var secondPayload map[string]any
	if err := json.Unmarshal(second.payload, &secondPayload); err != nil {
		t.Fatal(err)
	}
	if secondPayload["value"] != nil || secondPayload["raw_edt"] != nil || secondPayload["status"] != string(model.PropertyTimeout) || !second.retain {
		t.Fatalf("unavailable MQTT payload = %#v", secondPayload)
	}
	if broker.availabilityRetained() == 0 {
		t.Fatal("availability was not published with retain")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("publisher did not stop")
	}
}

func TestNewRejectsInvalidMQTTConfiguration(t *testing.T) {
	for _, config := range []Config{
		{BrokerURL: "http://example.test", TopicPrefix: "home"},
		{BrokerURL: "mqtt://example.test", TopicPrefix: "home/#"},
		{BrokerURL: "mqtt://example.test", TopicPrefix: "home", QoS: 3},
		{BrokerURL: "mqtt://example.test", TopicPrefix: "home", ReconnectMin: time.Second, ReconnectMax: time.Millisecond},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%#v) succeeded", config)
		}
	}
}

type publishedPacket struct {
	topic   string
	payload []byte
	retain  bool
}

type testBroker struct {
	listener           net.Listener
	packets            chan publishedPacket
	mu                 sync.Mutex
	availability       int
	closeFirstProperty bool
	closed             bool
}

func newTestBroker(t *testing.T, closeFirstProperty bool) *testBroker {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	broker := &testBroker{listener: listener, packets: make(chan publishedPacket, 16), closeFirstProperty: closeFirstProperty}
	go broker.accept()
	return broker
}

func (b *testBroker) address() string { return b.listener.Addr().String() }
func (b *testBroker) close() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		_ = b.listener.Close()
	}
	b.mu.Unlock()
}
func (b *testBroker) availabilityRetained() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.availability
}

func (b *testBroker) accept() {
	for {
		connection, err := b.listener.Accept()
		if err != nil {
			return
		}
		go b.handle(connection)
	}
}

func (b *testBroker) handle(connection net.Conn) {
	defer connection.Close()
	header, _, err := readTestPacket(connection)
	if err != nil || header != 0x10 {
		return
	}
	if _, err := connection.Write([]byte{0x20, 0x02, 0x00, 0x00}); err != nil {
		return
	}
	for {
		header, body, err := readTestPacket(connection)
		if err != nil {
			return
		}
		if header&0xF0 != 0x30 {
			continue
		}
		if len(body) < 2 {
			return
		}
		topicLength := int(binary.BigEndian.Uint16(body[:2]))
		if len(body) < 2+topicLength {
			return
		}
		topic := string(body[2 : 2+topicLength])
		payloadStart := 2 + topicLength
		qos := (header >> 1) & 3
		var id []byte
		if qos > 0 {
			if len(body) < payloadStart+2 {
				return
			}
			id = append([]byte(nil), body[payloadStart:payloadStart+2]...)
			payloadStart += 2
		}
		packet := publishedPacket{topic: topic, payload: append([]byte(nil), body[payloadStart:]...), retain: header&1 != 0}
		if topic == "home/availability" {
			b.mu.Lock()
			if packet.retain {
				b.availability++
			}
			b.mu.Unlock()
		} else {
			b.packets <- packet
		}
		if qos == 1 {
			if _, err := connection.Write(append([]byte{0x40, 0x02}, id...)); err != nil {
				return
			}
		}
		if qos == 2 {
			if _, err := connection.Write(append([]byte{0x50, 0x02}, id...)); err != nil {
				return
			}
			_, _, _ = readTestPacket(connection)
			if _, err := connection.Write(append([]byte{0x70, 0x02}, id...)); err != nil {
				return
			}
		}
		b.mu.Lock()
		closeNow := b.closeFirstProperty && topic != "home/availability"
		if closeNow {
			b.closeFirstProperty = false
		}
		b.mu.Unlock()
		if closeNow {
			return
		}
	}
}

func (b *testBroker) waitProperty(t *testing.T, timeout time.Duration) publishedPacket {
	t.Helper()
	select {
	case packet := <-b.packets:
		return packet
	case <-time.After(timeout):
		t.Fatal("timed out waiting for MQTT property publish")
		return publishedPacket{}
	}
}

func readTestPacket(reader io.Reader) (byte, []byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(reader, first[:]); err != nil {
		return 0, nil, err
	}
	length, err := decodeRemainingLength(reader)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, length)
	_, err = io.ReadFull(reader, body)
	return first[0], body, err
}
