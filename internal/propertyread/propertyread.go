// Package propertyread implements common ECHONET Lite GET property reads.
package propertyread

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/dayflower/echoview/internal/discover"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
)

// Config controls one or more attempts to read a property batch.
type Config struct {
	Timeout  time.Duration
	Attempts int
	Debugf   func(string, ...any)
	Waitf    func(string, ...any)
}

// Property is the wire-level result for one requested EPC.
type Property struct {
	EDT    []byte
	Status model.PropertyStatus
}

// ReadBatch requests epcs and returns a result for every requested EPC. Only
// timeouts are retried. The returned TID is advanced exactly once, regardless
// of how many attempts were made.
func ReadBatch(ctx context.Context, conn echonet.PacketConn, address netip.Addr, eoj echonet.EOJ, epcs []byte, tid uint16, config Config) (map[byte]Property, uint16) {
	result := unavailable(epcs, model.PropertyTimeout)
	packet, err := echonet.BuildGetRequest(tid, eoj, epcs)
	if err != nil {
		return result, tid + 1
	}
	expected := echonet.GetResponseExpectation(address, tid, eoj)
	for attempt := 1; attempt <= config.Attempts; attempt++ {
		waitf(config, "waiting up to %s for %s from %s %s", config.Timeout, formatEPCs(epcs), address, eoj)
		requestCtx, cancel := context.WithTimeout(ctx, config.Timeout)
		frame, err := (echonet.Transport{Conn: conn, Debugf: config.Debugf}).SendAndWait(requestCtx, netip.AddrPortFrom(address, discover.Port), packet, expected)
		cancel()
		if err != nil {
			if errors.Is(err, echonet.ErrTimeout) {
				debugf(config, "timed out reading %s from %s %s (attempt %d/%d)", formatEPCs(epcs), address, eoj, attempt, config.Attempts)
				continue
			}
			debugf(config, "could not read %s from %s %s: %v", formatEPCs(epcs), address, eoj, err)
			return result, tid + 1
		}
		responseStatus := model.PropertyNotReturned
		if frame.ESV == echonet.ESVGetSNA {
			responseStatus = model.PropertySNA
		}
		result = unavailable(epcs, responseStatus)
		for _, property := range frame.Properties {
			if _, wanted := result[property.EPC]; wanted {
				status := model.PropertyOK
				if frame.ESV == echonet.ESVGetSNA && len(property.EDT) == 0 {
					status = model.PropertySNA
				}
				var edt []byte
				if status == model.PropertyOK {
					edt = cloneEDT(property.EDT)
				}
				result[property.EPC] = Property{EDT: edt, Status: status}
			}
		}
		return result, tid + 1
	}
	return result, tid + 1
}

func cloneEDT(value []byte) []byte {
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func unavailable(epcs []byte, status model.PropertyStatus) map[byte]Property {
	result := make(map[byte]Property, len(epcs))
	for _, epc := range epcs {
		result[epc] = Property{Status: status}
	}
	return result
}

func debugf(config Config, format string, values ...any) {
	if config.Debugf != nil {
		config.Debugf(format, values...)
	}
}

func waitf(config Config, format string, values ...any) {
	if config.Waitf != nil {
		config.Waitf(format, values...)
	}
}

func formatEPCs(epcs []byte) string {
	result := ""
	for i, epc := range epcs {
		if i > 0 {
			result += ","
		}
		result += echonet.FormatEPC(epc)
	}
	return result
}
