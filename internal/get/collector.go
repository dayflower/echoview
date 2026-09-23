package get

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"

	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/model"
)

// Collector collects one configured instance at a time. It serializes request
// and response exchanges, including when each collection opens its own UDP
// connection.
type Collector struct {
	mu       sync.Mutex
	conn     echonet.PacketConn
	openConn ConnectionFactory
	config   Config
	nextTID  uint16
	verified map[collectionKey]string
}

type collectionKey struct {
	address netip.Addr
	eoj     echonet.EOJ
}

// Connection is a packet connection that can be closed to release its local
// UDP port and to interrupt a pending read during shutdown.
type Connection interface {
	echonet.PacketConn
	Close() error
}

// ConnectionFactory opens one ECHONET Lite connection for a collection.
type ConnectionFactory func() (Connection, error)

// NewCollector creates a collector suitable for use by a periodic scheduler.
func NewCollector(conn echonet.PacketConn, config Config) (*Collector, error) {
	if conn == nil {
		return nil, errors.New("ECHONET Lite collector has no packet connection")
	}
	if config.DataTimeout <= 0 || config.DataAttempts <= 0 {
		return nil, errors.New("data timeout and attempts must be greater than zero")
	}
	return &Collector{conn: conn, config: config, nextTID: config.InitialTID, verified: map[collectionKey]string{}}, nil
}

// NewPerCollectionCollector creates a collector that opens and closes a UDP
// connection around every collection. This leaves the ECHONET Lite port free
// while the scheduler is waiting for the next collection interval.
func NewPerCollectionCollector(open ConnectionFactory, config Config) (*Collector, error) {
	if open == nil {
		return nil, errors.New("ECHONET Lite collector has no connection factory")
	}
	if config.DataTimeout <= 0 || config.DataAttempts <= 0 {
		return nil, errors.New("data timeout and attempts must be greater than zero")
	}
	return &Collector{openConn: open, config: config, nextTID: config.InitialTID, verified: map[collectionKey]string{}}, nil
}

// Collect obtains the configured properties for one instance.
func (c *Collector) Collect(ctx context.Context, instance metricsconfig.Instance) (model.InstanceResult, error) {
	if err := ctx.Err(); err != nil {
		return model.InstanceResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return model.InstanceResult{}, err
	}
	conn := c.conn
	var closeConn Connection
	if c.openConn != nil {
		opened, err := c.openConn()
		if err != nil {
			return model.InstanceResult{}, fmt.Errorf("open ECHONET Lite connection: %w", err)
		}
		conn, closeConn = opened, opened
		done := make(chan struct{})
		defer close(done)
		defer closeConn.Close()
		go func() {
			select {
			case <-ctx.Done():
				_ = closeConn.Close()
			case <-done:
			}
		}()
	}

	applied, err := selectedProfile(instance, c.config.Profiles)
	if err != nil {
		return model.InstanceResult{}, err
	}
	key := collectionKey{address: instance.Address, eoj: instance.EOJ}
	matchState, verified := c.verified[key]
	if !verified {
		matchState, c.nextTID, err = verifyProfile(ctx, conn, instance, c.nextTID, applied, c.config)
		if err != nil {
			return model.InstanceResult{}, err
		}
		c.verified[key] = matchState
	}
	result, nextTID := readInstance(ctx, conn, instance, c.nextTID, applied, matchState, c.config)
	c.nextTID = nextTID
	return result, nil
}
