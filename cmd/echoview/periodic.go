package main

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"time"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	getcmd "github.com/dayflower/echoview/internal/get"
	"github.com/dayflower/echoview/internal/metricsconfig"
	"github.com/dayflower/echoview/internal/profile"
	"github.com/dayflower/echoview/internal/scheduler"
)

// periodicOptions contains the flags shared by commands that continuously
// collect configured ECHONET Lite instances.
type periodicOptions struct {
	interfaceAddress string
	configPath       string
	catalogDirs      catalogDirectories
	dataTimeout      time.Duration
	dataAttempts     int
	keepBinding      bool
	quiet            bool
	debug            bool
}

func (options periodicOptions) validDataCollection() bool {
	return options.dataTimeout > 0 && options.dataAttempts > 0
}

// periodicConfiguration is the common, validated input shared by periodic
// commands. Loading it before opening a socket keeps configuration errors
// separate from ECHONET Lite initialization errors.
type periodicConfiguration struct {
	catalog  *catalog.Catalog
	profiles *profile.Catalog
	metrics  *metricsconfig.Config
}

func loadPeriodicConfiguration(options periodicOptions, logger *stderrLogger) (*periodicConfiguration, error) {
	set, err := loadCatalogSet(options.catalogDirs, logger)
	if err != nil {
		return nil, fmt.Errorf("invalid catalog: %w", err)
	}
	loadedMetrics, err := metricsconfig.Load(options.configPath, set.Profiles)
	if err != nil {
		return nil, fmt.Errorf("invalid metrics configuration: %w", err)
	}
	return &periodicConfiguration{catalog: set.Catalog, profiles: set.Profiles, metrics: loadedMetrics}, nil
}

// periodicRuntime owns the collector and, when requested, the UDP connection
// that remains bound between collections.
type periodicRuntime struct {
	configuration *periodicConfiguration
	locale        catalog.Locale
	logger        *stderrLogger
	collector     *getcmd.Collector

	connection     io.Closer
	connectionOnce sync.Once
}

func newPeriodicRuntime(localAddress netip.Addr, options periodicOptions, configuration *periodicConfiguration, stderr io.Writer, dependencies cliDependencies) (*periodicRuntime, error) {
	logger := newStderrLoggerAt(stderr, options.quiet, options.debug, dependencies.now)
	collectorConfig := getcmd.Config{
		DataTimeout:  options.dataTimeout,
		DataAttempts: options.dataAttempts,
		InitialTID:   uint16(dependencies.now().UnixNano()),
		Profiles:     configuration.profiles,
		Catalog:      configuration.catalog,
		Locale:       catalog.LocaleFromEnvironment(),
	}
	if options.debug {
		collectorConfig.Debugf = logger.Debugf
	}

	runtime := &periodicRuntime{configuration: configuration, locale: collectorConfig.Locale, logger: logger}
	var err error
	if options.keepBinding {
		connection, openErr := openECHONETConnection(localAddress, dependencies)
		err = openErr
		runtime.connection = connection
		if err == nil {
			runtime.collector, err = getcmd.NewCollector(connection, collectorConfig)
		}
	} else {
		runtime.collector, err = getcmd.NewPerCollectionCollector(func() (getcmd.Connection, error) {
			return openECHONETConnection(localAddress, dependencies)
		}, collectorConfig)
	}
	if err != nil {
		runtime.Close()
		if options.keepBinding && runtime.connection == nil {
			return nil, fmt.Errorf("open ECHONET Lite socket on %s:%d: %w", localAddress, discover.Port, err)
		}
		return nil, fmt.Errorf("initialize ECHONET Lite collector: %w", err)
	}
	return runtime, nil
}

// Close releases the persistent socket, if this runtime owns one. It is safe
// to call more than once so shutdown paths can share the same owner.
func (runtime *periodicRuntime) Close() {
	if runtime == nil || runtime.connection == nil {
		return
	}
	runtime.connectionOnce.Do(func() { _ = runtime.connection.Close() })
}

// CloseOnCancel releases a persistent socket as soon as ctx is canceled. The
// returned function must be called once the command no longer needs the
// watcher; doing so prevents a shutdown goroutine from outliving the command.
func (runtime *periodicRuntime) CloseOnCancel(ctx context.Context) func() {
	if runtime == nil || runtime.connection == nil {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			runtime.Close()
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
		})
	}
}

func (runtime *periodicRuntime) NewScheduler(observers ...scheduler.CollectionObserver) (*scheduler.Scheduler, error) {
	targets := make([]scheduler.Target, len(runtime.configuration.metrics.Instances))
	for i, instance := range runtime.configuration.metrics.Instances {
		targets[i] = scheduler.TargetFromInstance(instance)
	}
	return scheduler.New(scheduler.Config{
		Targets:   targets,
		Collector: runtime.collector,
		Infof:     runtime.logger.Infof,
		Observers: observers,
	})
}
