# Grafana Dashboard for My CSI Driver

This directory contains a Grafana dashboard for monitoring the My CSI Driver metrics.

## Dashboard Overview

The dashboard (`grafana-dashboard.json`) provides comprehensive visualization of CSI driver metrics including:

### Panels

1. **Remaining Capacity by Node** - Time series showing free capacity on each node
2. **Current Remaining Capacity** - Bar gauge showing current available capacity per node
3. **Volume Total Size** - Time series of allocated disk space per volume
4. **Volume Used Space** - Time series of actual disk usage per volume
5. **Volume Usage Percentage** - Percentage of used vs. allocated space per volume
6. **Volume Count per Node** - Number of volumes on each node
7. **Total Used Storage per Node** - Gauge showing total used storage across all volumes
8. **Total Allocated Storage per Node** - Gauge showing total allocated storage across all volumes

### Metrics Used

The dashboard visualizes the following Prometheus metrics:

- `rawfile_remaining_capacity{node}` - Free capacity for new volumes on each node (bytes)
- `rawfile_volume_used{node,volume}` - Actual disk space used by each volume (bytes)
- `rawfile_volume_total{node,volume}` - Total disk space allocated to each volume (bytes)

## Installation

### Option 1: Import via Grafana UI

1. Log into your Grafana instance
2. Navigate to **Dashboards** → **Import**
3. Click **Upload JSON file**
4. Select `grafana-dashboard.json`
5. Choose your Prometheus datasource
6. Click **Import**

### Option 2: Import via API

```bash
# Set your Grafana URL and API key
GRAFANA_URL="http://localhost:3000"
GRAFANA_API_KEY="your-api-key"

# Import the dashboard
curl -X POST "${GRAFANA_URL}/api/dashboards/db" \
  -H "Authorization: Bearer ${GRAFANA_API_KEY}" \
  -H "Content-Type: application/json" \
  -d @grafana-dashboard.json
```

### Option 3: ConfigMap for Kubernetes

If you're running Grafana in Kubernetes with dashboard auto-discovery:

```bash
kubectl create configmap my-csi-driver-dashboard \
  --from-file=grafana-dashboard.json \
  --namespace=monitoring

# Add the label for auto-discovery
kubectl label configmap my-csi-driver-dashboard \
  grafana_dashboard=1 \
  --namespace=monitoring
```

## Configuration

The dashboard uses a templated datasource variable, allowing you to select any Prometheus datasource in your Grafana instance.

### Time Range

- Default: Last 6 hours
- Auto-refresh: Every 30 seconds

### Tags

The dashboard is tagged with:
- `csi`
- `storage`
- `volumes`

## Metrics Collection

Ensure that:

1. The CSI driver is running with metrics enabled (default port 9898)
2. Prometheus is configured to scrape the CSI driver metrics endpoint
3. The Helm chart has been deployed with `metrics.enabled=true` (default)

### Prometheus ServiceMonitor Example

If using Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: my-csi-driver-metrics
  namespace: kube-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: my-csi-driver
      app.kubernetes.io/component: node
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

### Manual Prometheus Configuration

Add to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'my-csi-driver'
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - kube-system
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
        action: keep
        regex: true
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
        action: replace
        target_label: __metrics_path__
        regex: (.+)
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        action: replace
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: $1:$2
        target_label: __address__
```

## Customization

You can customize the dashboard by:

1. Modifying thresholds for gauges and alerts
2. Adjusting time ranges and refresh intervals
3. Adding additional panels for custom queries
4. Changing visualization types

## Troubleshooting

### No Data Displayed

1. Verify metrics are being exposed:
   ```bash
   kubectl port-forward -n kube-system daemonset/my-csi-driver 9898:9898
   curl http://localhost:9898/metrics
   ```

2. Check Prometheus is scraping the metrics:
   - Navigate to Prometheus UI → Status → Targets
   - Look for `my-csi-driver` job
   - Verify targets are UP

3. Verify datasource configuration in Grafana:
   - Go to Configuration → Data Sources
   - Test the Prometheus datasource connection

### Metrics Not Updating

- Check the auto-refresh setting (default: 30s)
- Verify the time range covers recent data
- Ensure the CSI driver pods are running and healthy

## Support

For issues related to:
- **Dashboard**: Check this README and Grafana documentation
- **Metrics**: See the CSI driver documentation and metrics implementation
- **Prometheus**: Refer to Prometheus documentation for scraping configuration
