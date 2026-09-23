// Package mqtt publishes periodic ECHONET Lite collection results over MQTT.
package mqtt

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
	"github.com/dayflower/echoview/internal/scheduler"
)

// Config controls MQTT connection and publication behavior.
type Config struct {
	BrokerURL         string
	TopicPrefix       string
	AvailabilityTopic string
	ClientID          string
	Username          string
	Password          string
	QoS               byte
	Retain            bool
	TLSConfig         *tls.Config
	ReconnectMin      time.Duration
	ReconnectMax      time.Duration
	Instances         []metricsconfig.Instance
}

// Payload is the retained JSON representation for one ECHONET Lite property.
// Value and RawEDT deliberately remain present when nil so unavailable values
// replace previously retained readings rather than leaving stale state behind.
type Payload struct {
	Value       any                  `json:"value"`
	RawEDT      *string              `json:"raw_edt"`
	Unit        *string              `json:"unit"`
	CollectedAt time.Time            `json:"collected_at"`
	Status      model.PropertyStatus `json:"status"`
}

// Publisher receives scheduler events without blocking collection. While the
// broker is unavailable it retains only the most recent snapshot.
type Publisher struct {
	config     Config
	instances  map[string]metricsconfig.Instance
	mu         sync.Mutex
	latest     *scheduler.CollectionEvent
	generation uint64
	wake       chan struct{}
}

// New validates configuration and constructs an event observer.
func New(config Config) (*Publisher, error) {
	if config.BrokerURL == "" {
		return nil, fmt.Errorf("MQTT broker URL is required")
	}
	if _, err := parseBrokerURL(config.BrokerURL); err != nil {
		return nil, err
	}
	config.TopicPrefix = strings.Trim(config.TopicPrefix, "/")
	if config.TopicPrefix == "" || strings.ContainsAny(config.TopicPrefix, "+#") {
		return nil, fmt.Errorf("MQTT topic prefix must be non-empty and cannot contain wildcards")
	}
	if config.AvailabilityTopic == "" {
		config.AvailabilityTopic = config.TopicPrefix + "/availability"
	}
	config.AvailabilityTopic = strings.Trim(config.AvailabilityTopic, "/")
	if config.AvailabilityTopic == "" || strings.ContainsAny(config.AvailabilityTopic, "+#") {
		return nil, fmt.Errorf("MQTT availability topic must be non-empty and cannot contain wildcards")
	}
	if config.ClientID == "" {
		config.ClientID = "echoview"
	}
	if err := ValidateClientID(config.ClientID); err != nil {
		return nil, err
	}
	if config.QoS > 2 {
		return nil, fmt.Errorf("MQTT QoS must be 0, 1, or 2")
	}
	if config.ReconnectMin <= 0 {
		config.ReconnectMin = time.Second
	}
	if config.ReconnectMax <= 0 {
		config.ReconnectMax = 30 * time.Second
	}
	if config.ReconnectMax < config.ReconnectMin {
		return nil, fmt.Errorf("MQTT reconnect maximum must not be less than minimum")
	}
	instances := make(map[string]metricsconfig.Instance, len(config.Instances))
	for _, instance := range config.Instances {
		key := instanceKey(instance.Address.String(), instance.EOJ.String())
		if _, exists := instances[key]; exists {
			return nil, fmt.Errorf("duplicate MQTT instance %s", key)
		}
		instances[key] = instance
	}
	return &Publisher{config: config, instances: instances, wake: make(chan struct{}, 1)}, nil
}

// ObserveCollection records the event and wakes the publisher. It never waits
// for broker I/O, so a broker outage cannot delay the collection scheduler.
func (p *Publisher) ObserveCollection(event scheduler.CollectionEvent) {
	p.mu.Lock()
	p.latest = &event
	p.generation++
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run maintains the broker connection until ctx is canceled. A connection
// publishes availability online and the newest complete snapshot; subsequent
// scheduler events publish only their completed instance.
func (p *Publisher) Run(ctx context.Context) error {
	delay := p.config.ReconnectMin
	for {
		if ctx.Err() != nil {
			return nil
		}
		client, err := dial(ctx, p.config)
		if err != nil {
			if !wait(ctx, delay) {
				return nil
			}
			delay = nextDelay(delay, p.config.ReconnectMax)
			continue
		}
		delay = p.config.ReconnectMin
		lastGeneration := uint64(0)
		err = client.publish(p.config.AvailabilityTopic, []byte("online"), 1, true)
		if err == nil {
			event, generation := p.current()
			if event != nil {
				err = p.publishSnapshot(client, event.Snapshot)
				lastGeneration = generation
			}
		}
		if err != nil {
			_ = client.abort()
			if !wait(ctx, delay) {
				return nil
			}
			delay = nextDelay(delay, p.config.ReconnectMax)
			continue
		}
		for err == nil && ctx.Err() == nil {
			select {
			case <-ctx.Done():
				_ = client.publish(p.config.AvailabilityTopic, []byte("offline"), 1, true)
				_ = client.close()
				return nil
			case <-p.wake:
				event, generation := p.current()
				if event == nil || generation == lastGeneration {
					continue
				}
				err = p.publishInstance(client, event.Instance)
				if err == nil {
					lastGeneration = generation
				}
			}
		}
		_ = client.abort()
		if !wait(ctx, delay) {
			return nil
		}
		delay = nextDelay(delay, p.config.ReconnectMax)
	}
}

func (p *Publisher) current() (*scheduler.CollectionEvent, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.latest == nil {
		return nil, p.generation
	}
	copy := *p.latest
	return &copy, p.generation
}

func (p *Publisher) publishSnapshot(client *client, snapshot scheduler.Snapshot) error {
	for _, item := range snapshot.Instances {
		if !item.Completed {
			continue
		}
		if err := p.publishInstance(client, item); err != nil {
			return err
		}
	}
	return nil
}

func (p *Publisher) publishInstance(client *client, item scheduler.InstanceSnapshot) error {
	instance, exists := p.instances[instanceKey(item.Address, item.EOJ.String())]
	if !exists {
		return nil
	}
	properties := make(map[string]model.PropertyResult, len(item.Result.GetProperties))
	if item.Succeeded {
		for _, property := range item.Result.GetProperties {
			properties[property.EPC] = property
		}
	}
	for _, configured := range instance.Properties {
		epc := echonet.FormatEPC(configured.EPC)
		property, found := properties[epc]
		if !found {
			property = model.PropertyResult{EPC: epc, Status: model.PropertyNotReturned}
		}
		if property.Status != model.PropertyOK {
			property.Value = nil
			property.RawEDT = nil
			property.Unit = nil
		}
		payload, err := json.Marshal(Payload{Value: property.Value, RawEDT: property.RawEDT, Unit: property.Unit, CollectedAt: item.CollectedAt, Status: property.Status})
		if err != nil {
			return fmt.Errorf("encode MQTT payload for %s: %w", epc, err)
		}
		topic := strings.Join([]string{p.config.TopicPrefix, instance.MetricPrefix, item.EOJ.String(), epc}, "/")
		if err := client.publish(topic, payload, p.config.QoS, p.config.Retain); err != nil {
			return err
		}
	}
	return nil
}

func instanceKey(address, eoj string) string { return address + "/" + eoj }

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func parseBrokerURL(value string) (*url.URL, error) {
	if !strings.Contains(value, "://") {
		value = "mqtt://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("invalid MQTT broker URL %q", value)
	}
	if parsed.User != nil || parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("MQTT broker URL must contain only scheme and host")
	}
	switch parsed.Scheme {
	case "mqtt", "tcp", "mqtts", "ssl":
		return parsed, nil
	default:
		return nil, fmt.Errorf("unsupported MQTT broker URL scheme %q", parsed.Scheme)
	}
}

// Topics returns the deterministic property topics configured for the publisher.
// It is primarily useful for diagnostics and tests.
func (p *Publisher) Topics() []string {
	topics := make([]string, 0)
	for _, instance := range p.instances {
		for _, property := range instance.Properties {
			topics = append(topics, strings.Join([]string{p.config.TopicPrefix, instance.MetricPrefix, instance.EOJ.String(), echonet.FormatEPC(property.EPC)}, "/"))
		}
	}
	sort.Strings(topics)
	return topics
}
