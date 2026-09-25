# Echoview Prometheus exporter Helm chart

This chart runs one Echoview Prometheus exporter on a node that can reach
ECHONET Lite devices. It uses host networking for UDP port 3610 and exposes
Prometheus metrics on TCP port 13610 by default. The image includes the base
catalog and example vendor profiles. No Echoview installation on the node is
needed.

## Install on Kubernetes

Use `kubectl` and Helm 3 with access to the target cluster. The chart exposes
metrics through a ClusterIP Service. ServiceMonitor creation is optional and
disabled by default.

Generate `instances.yaml` with `dump --format metrics-config`, then review its
devices and properties. The file starts with `instances_format: 2`: this is
Echoview's current configuration schema version, not a YAML version or a Helm
setting. The `dump` command writes it automatically. See the
[configuration contract](../../notes/metrics-config-contract.md) for the full
shape. Keep device addresses in the local file rather than committing them to
the repository.

The default `nodeSelector` is empty, so a single-node cluster needs no node
label. On a multi-node cluster, set `nodeSelector` to choose a node that can
reach the ECHONET Lite devices. Host networking uses the selected node's LAN;
it does not select a suitable node automatically. Check node names with
`kubectl get nodes` and use a values file as shown below.

Create a ConfigMap from the reviewed configuration, then install the chart.
Run these commands from the repository root with `instances.yaml` in that
directory.

```sh
kubectl create namespace echoview
kubectl -n echoview create configmap echoview-instances --from-file=instances.yaml --dry-run=client -o yaml | kubectl -n echoview apply -f -
helm upgrade --install echoview-prometheus ./charts/echoview-prometheus --namespace echoview
```

The chart's default `instancesConfig.existingConfigMap` is
`echoview-instances`, and its default key is `instances.yaml`. If the ConfigMap
already has another name or key, set `instancesConfig.existingConfigMap` and
`instancesConfig.key` in a values file. For example:

```yaml
instancesConfig:
  existingConfigMap: home-echoview
  key: devices.yaml
nodeSelector:
  kubernetes.io/hostname: my-node
```

Pass the file with `--values my-values.yaml`. Keep the ConfigMap and release
in the same namespace. Updating an existing ConfigMap requires a Deployment
restart because Echoview reads configuration only at startup:

```sh
kubectl -n echoview rollout restart deployment/echoview-prometheus
```

Alternatively, have Helm create the ConfigMap from the local file. In this
mode, do not create `echoview-instances` separately:

```sh
helm upgrade --install echoview-prometheus ./charts/echoview-prometheus \
  --namespace echoview --create-namespace \
  --set-string instancesConfig.existingConfigMap= \
  --set-file instancesConfig.content=instances.yaml
```

The chart-managed ConfigMap name defaults to
`echoview-prometheus-instances`. Set `instancesConfig.name` to change it. An
upgrade with changed content restarts the Pod automatically.

## Values

| Value | Default | Purpose |
| --- | --- | --- |
| `nodeSelector` | `{}` | Optionally select a node with access to the devices. Set this on multi-node clusters. |
| `instancesConfig.existingConfigMap` | `echoview-instances` | Read an existing ConfigMap; empty means Helm creates one. |
| `instancesConfig.name` | empty | Name of the Helm-created ConfigMap. |
| `instancesConfig.key` | `instances.yaml` | ConfigMap key and filename mounted in the Pod. |
| `instancesConfig.content` | empty | File content for a Helm-created ConfigMap. |
| `catalog.existingConfigMap` | empty | Mount an existing ConfigMap with additional catalog YAML files. |
| `commonLabels` | empty | Add labels to chart resources. |
| `podLabels` | empty | Add labels to the exporter Pod. |
| `service.labels` | empty | Add labels to the metrics Service. |
| `serviceMonitor.enabled` | `false` | Create a ServiceMonitor when Prometheus Operator is configured to discover it. |
| `serviceMonitor.labels` | `release: kube-prom-stack` | Match the Prometheus ServiceMonitor selector. |
| `serviceMonitor.interval` | `60s` | Prometheus scrape interval. |
| `exporter.listenPort` | `13610` | TCP port bound on the node. |
| `exporter.interfaceAddress` | empty | Optional local IPv4 address for ECHONET Lite traffic. |
| `exporter.dataTimeout` | `20s` | Timeout for one property GET. |
| `exporter.dataAttempts` | `1` | Total property GET attempts. |
| `service.port` | `13610` | ClusterIP Service port. |

Use a positive Go duration for `exporter.dataTimeout` and a positive integer
for `exporter.dataAttempts`. For example, set `30s` and `2` to allow a slower
device more time and one retry.

To add custom class or profile definitions, create a ConfigMap with YAML files
and set `catalog.existingConfigMap` to its name:

```sh
kubectl -n echoview create configmap echoview-catalog \
  --from-file=./my-profile.yaml --dry-run=client -o yaml | \
  kubectl -n echoview apply -f -
```

```yaml
catalog:
  existingConfigMap: echoview-catalog
exporter:
  dataTimeout: 30s
  dataAttempts: 2
```

The files are mounted in Echoview's default user catalog directory alongside
the image's bundled catalog directory. Each ConfigMap key must end in `.yaml`
or `.yml`. Updating the ConfigMap requires a Deployment restart because
Echoview loads catalogs only at startup. A profile is used only when referenced
by `instances.yaml`.

The chart keeps its Pod and Service selector labels stable. Additional labels
can be changed independently without breaking that connection.

## Enable ServiceMonitor

To use a ServiceMonitor, the cluster needs Prometheus Operator or a compatible
installation that provides the ServiceMonitor CRD and manages a Prometheus
instance. That instance must select ServiceMonitors from the `echoview`
namespace and match the labels on this chart's ServiceMonitor. Check the CRD
and the Prometheus resource before enabling it. See the
[Prometheus Operator documentation](https://prometheus-operator.dev/docs/getting-started/design/)
for how ServiceMonitor selection works.

```sh
kubectl get crd servicemonitors.monitoring.coreos.com
kubectl get prometheus -A
kubectl -n PROMETHEUS_NAMESPACE get prometheus PROMETHEUS_NAME -o yaml
```

Inspect `spec.serviceMonitorSelector` and
`spec.serviceMonitorNamespaceSelector` in that resource. The chart's default
ServiceMonitor label is `release: kube-prom-stack`; change
`serviceMonitor.labels` if the label selector requires something else. The
namespace selector must include the namespace where this chart is installed.
For example, add this to a values file when those selectors match:

```yaml
serviceMonitor:
  enabled: true
  labels:
    release: kube-prom-stack
```

Apply the file with:

```sh
helm upgrade --install echoview-prometheus ./charts/echoview-prometheus \
  --namespace echoview --values my-values.yaml
```

If Prometheus uses another scrape configuration, leave ServiceMonitor disabled.

## Verify and operate

```sh
kubectl -n echoview rollout status deployment/echoview-prometheus
kubectl -n echoview get endpointslice -l kubernetes.io/service-name=echoview-prometheus
kubectl -n echoview port-forward service/echoview-prometheus 13610:13610
```

In another terminal, run `curl http://127.0.0.1:13610/metrics`. Once a
ServiceMonitor or another scrape configuration is active, check the Prometheus
Targets page for an `UP` Echoview target. An `UP` target confirms that scraping
works; after the first collection, check
`echonet_lite_collection_success` for device reads. `/healthz` reports HTTP
server health, not device health.

The Deployment uses `Recreate` and one replica so upgrades do not overlap on
the node's TCP port. With host networking, `0.0.0.0:13610` also listens on
the node's LAN address unless a host firewall blocks it. A ClusterIP Service
does not close that direct node port. To avoid a TCP port conflict, change
`exporter.listenPort`; `service.port` can remain 13610. Restrict TCP access
at the node firewall if LAN clients should not read metrics. NetworkPolicy
behavior for host-networked Pods depends on the network implementation.
