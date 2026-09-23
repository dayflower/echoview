package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	mqttpublish "github.com/dayflower/echoview/internal/mqtt"
)

func runMQTTWithDependencies(args []string, stdout, stderr io.Writer, dependencies cliDependencies) int {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			printMQTTUsage(stdout)
			return 0
		}
	}
	flags := flag.NewFlagSet("mqtt", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printMQTTUsage(stderr) }
	options := periodicOptions{dataTimeout: 20 * time.Second, dataAttempts: 1}
	var brokerURL, topicPrefix, availabilityTopic, clientID, username, password string
	var tlsCA, tlsCert, tlsKey string
	var reconnectMin, reconnectMax time.Duration
	var qos int
	var retain, insecureTLS bool
	flags.StringVar(&options.interfaceAddress, "interface", "", "local IPv4 address used for ECHONET Lite traffic")
	flags.StringVar(&options.configPath, "config", "", "format-2 metrics instance configuration path")
	flags.Var(&options.catalogDirs, "catalog-dir", "catalog directory; repeatable")
	flags.DurationVar(&options.dataTimeout, "data-timeout", options.dataTimeout, "timeout for one property GET")
	flags.IntVar(&options.dataAttempts, "data-attempts", options.dataAttempts, "total property GET attempts")
	flags.BoolVar(&options.keepBinding, "keep-binding", false, "keep UDP port 3610 bound between collections")
	flags.StringVar(&brokerURL, "broker", "", "MQTT broker URL, for example mqtt://broker:1883")
	flags.StringVar(&topicPrefix, "topic-prefix", "echonet_lite", "MQTT topic prefix")
	flags.StringVar(&availabilityTopic, "availability-topic", "", "retained MQTT availability topic")
	flags.StringVar(&clientID, "client-id", "echoview", "MQTT client ID")
	flags.StringVar(&username, "username", "", "MQTT username")
	flags.StringVar(&password, "password", "", "MQTT password")
	flags.IntVar(&qos, "qos", 1, "MQTT QoS: 0, 1, or 2")
	flags.BoolVar(&retain, "retain", true, "retain property payloads")
	flags.StringVar(&tlsCA, "tls-ca", "", "PEM certificate authority bundle for mqtts")
	flags.StringVar(&tlsCert, "tls-cert", "", "PEM client certificate for mqtts")
	flags.StringVar(&tlsKey, "tls-key", "", "PEM client key for mqtts")
	flags.BoolVar(&insecureTLS, "tls-insecure-skip-verify", false, "disable mqtts server certificate verification")
	flags.DurationVar(&reconnectMin, "reconnect-min", time.Second, "initial MQTT reconnect delay")
	flags.DurationVar(&reconnectMax, "reconnect-max", 30*time.Second, "maximum MQTT reconnect delay")
	flags.BoolVar(&options.quiet, "quiet", false, "suppress non-fatal waiting, info, and warning logs")
	flags.BoolVar(&options.debug, "debug", false, "write detailed diagnostics to standard error")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "error: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	if options.configPath == "" || brokerURL == "" {
		fmt.Fprintln(stderr, "error: --config and --broker are required")
		return 2
	}
	if !options.validDataCollection() || qos < 0 || qos > 2 || reconnectMin <= 0 || reconnectMax < reconnectMin {
		fmt.Fprintln(stderr, "error: data timeout/attempts, QoS, and reconnect delays are invalid")
		return 2
	}
	if (tlsCert == "") != (tlsKey == "") {
		fmt.Fprintln(stderr, "error: --tls-cert and --tls-key must be used together")
		return 2
	}
	logger := newStderrLoggerAt(stderr, options.quiet, options.debug, dependencies.now)
	configuration, err := loadPeriodicConfiguration(options, logger)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	tlsConfig, err := mqttTLSConfig(tlsCA, tlsCert, tlsKey, insecureTLS)
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid MQTT TLS configuration: %v\n", err)
		return 2
	}
	publisher, err := mqttpublish.New(mqttpublish.Config{BrokerURL: brokerURL, TopicPrefix: topicPrefix, AvailabilityTopic: availabilityTopic, ClientID: clientID, Username: username, Password: password, QoS: byte(qos), Retain: retain, TLSConfig: tlsConfig, ReconnectMin: reconnectMin, ReconnectMax: reconnectMax, Instances: configuration.metrics.Instances})
	if err != nil {
		fmt.Fprintf(stderr, "error: invalid MQTT configuration: %v\n", err)
		return 2
	}
	localAddress, err := dependencies.localIPv4(options.interfaceAddress)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}
	runtime, err := newPeriodicRuntime(localAddress, options, configuration, stderr, dependencies)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 4
	}
	defer runtime.Close()
	runner, err := runtime.NewScheduler(publisher)
	if err != nil {
		fmt.Fprintf(stderr, "error: initialize collection scheduler: %v\n", err)
		return 4
	}
	signalContext, stop := dependencies.signalContext(context.Background())
	defer stop()
	ctx, cancel := context.WithCancel(signalContext)
	defer cancel()
	stopConnectionWatcher := runtime.CloseOnCancel(ctx)
	defer stopConnectionWatcher()
	mqttDone := make(chan error, 1)
	go func() { mqttDone <- publisher.Run(ctx) }()
	runDone := make(chan error, 1)
	go func() { runDone <- runner.Run(ctx) }()
	select {
	case err := <-runDone:
		cancel()
		<-mqttDone
		if err != nil {
			fmt.Fprintf(stderr, "error: collection scheduler failed: %v\n", err)
			return 4
		}
	case <-signalContext.Done():
		cancel()
		<-runDone
		<-mqttDone
	}
	return 0
}

func mqttTLSConfig(caPath, certPath, keyPath string, insecure bool) (*tls.Config, error) {
	if caPath == "" && certPath == "" && !insecure {
		return nil, nil
	}
	result := &tls.Config{InsecureSkipVerify: insecure} // #nosec G402 -- explicitly selected by the CLI user.
	if caPath != "" {
		data, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("read PEM certificates from %q", caPath)
		}
		result.RootCAs = pool
	}
	if certPath != "" {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, err
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func printMQTTUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: echoview mqtt --config PATH --broker URL [options]")
	fmt.Fprintln(output, "\nPublishes each completed collection to retained JSON topics and keeps collection running while MQTT reconnects.")
	fmt.Fprintln(output, "\nOptions:")
	fmt.Fprintln(output, "  --interface ADDRESS")
	fmt.Fprintln(output, "  --config PATH (required)")
	fmt.Fprintln(output, "  --catalog-dir DIRECTORY (repeatable; replaces default catalog directories)")
	fmt.Fprintln(output, "  --data-timeout DURATION (default 20s)")
	fmt.Fprintln(output, "  --data-attempts COUNT (default 1)")
	fmt.Fprintln(output, "  --keep-binding")
	fmt.Fprintln(output, "  --broker URL (required; mqtt:// or mqtts://)")
	fmt.Fprintln(output, "  --topic-prefix TOPIC (default echonet_lite)")
	fmt.Fprintln(output, "  --availability-topic TOPIC (default TOPIC/availability)")
	fmt.Fprintln(output, "  --client-id ID --username USER --password PASSWORD")
	fmt.Fprintln(output, "  --qos 0|1|2 (default 1) --retain=true|false")
	fmt.Fprintln(output, "  --tls-ca PATH --tls-cert PATH --tls-key PATH --tls-insecure-skip-verify")
	fmt.Fprintln(output, "  --reconnect-min DURATION (default 1s) --reconnect-max DURATION (default 30s)")
	fmt.Fprintln(output, "  --quiet (suppress non-fatal waiting, info, and warning logs)")
	fmt.Fprintln(output, "  --debug")
}
