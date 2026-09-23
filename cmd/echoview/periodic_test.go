package main

import (
	"context"
	"testing"
	"time"
)

type closeRecorder struct{ closed chan struct{} }

func (recorder *closeRecorder) Close() error {
	select {
	case <-recorder.closed:
	default:
		close(recorder.closed)
	}
	return nil
}

func TestPeriodicRuntimeClosesPersistentConnectionOnCancel(t *testing.T) {
	connection := &closeRecorder{closed: make(chan struct{})}
	runtime := &periodicRuntime{connection: connection}
	ctx, cancel := context.WithCancel(context.Background())
	stop := runtime.CloseOnCancel(ctx)
	cancel()
	t.Cleanup(stop)

	select {
	case <-connection.closed:
	case <-time.After(time.Second):
		t.Fatal("persistent connection was not closed after cancellation")
	}
	runtime.Close()
	if err := connection.Close(); err != nil {
		t.Fatalf("second close error = %v", err)
	}
}

func TestPeriodicRuntimeCloseOnCancelWithoutPersistentConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := (&periodicRuntime{}).CloseOnCancel(ctx)
	stop()
	stop()
}
