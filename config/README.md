# Secret Controller Deployment Guide

This guide walks you through deploying the secret controller to your Kubernetes cluster.

## Prerequisites

1. **Kueue installed** with MultiKueue support enabled
2. **Docker/Podman** for building container images
3. **kubectl** configured for your hub cluster
4. **Proper RBAC permissions** to create cluster-scoped resources

## Quick Deployment

### 1. Build and Push Container Image

```bash
# Build the image
docker build -t zakisk/secret-controller:latest .

# Push to your registry
docker push zakisk/secret-controller:latest
```

### 2. Update Image Reference

Edit `deploy/deployment.yaml` and replace `secret-controller:latest` with your actual image:

```yaml
containers:
  - name: controller
    image: your-registry/secret-controller:latest # Update this line
```

### 3. Create Namespace (if needed)

```bash
kubectl create namespace kueue-system --dry-run=client -o yaml | kubectl apply -f -
```

### 4. Deploy RBAC and Controller

```bash
# Apply RBAC
kubectl apply -f deploy/rbac.yaml

# Deploy the controller
kubectl apply -f deploy/deployment.yaml
```

### 5. Verify Deployment

```bash
# Check if pod is running
kubectl get pods -n kueue-system -l app=secret-controller

# Check logs
kubectl logs -n kueue-system -l app=secret-controller -f
```

## Configuration

### Setting up MultiKueue CRDs

1. **Create kubeconfig secrets** for each spoke cluster:

```bash
# Create secret with spoke cluster kubeconfig
kubectl create secret generic spoke-cluster-1-kubeconfig \
  --from-file=kubeconfig=path/to/spoke-cluster-1-kubeconfig.yaml \
  -n kueue-system
```

2. **Apply MultiKueue CRDs**:

```bash
kubectl apply -f examples/multikueue-setup.yaml
```

### Testing Secret Sync

1. **Create a test secret** with the sync label:

```bash
kubectl apply -f examples/sample-secret.yaml
```

2. **Check controller logs** to see sync activity:

```bash
kubectl logs -n kueue-system -l app=secret-controller -f
```

3. **Verify secret was created** on spoke clusters:

```bash
# Check on spoke cluster
kubectl get secrets pipelines-secret -n default
```

## Customization

### Changing Sync Label

To change which secrets get synced, modify the constants in `reconciler/controller.go`:

```go
const (
    syncLabelKey   = "your-label-key"
    syncLabelValue = "your-label-value"
)
```

### Resource Limits

Adjust resource requests/limits in `deploy/deployment.yaml`:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### Replica Count

For high availability, increase replicas in `deploy/deployment.yaml`:

```yaml
spec:
  replicas: 2 # Increase for HA
```

## Troubleshooting

### Common Issues

1. **Pod not starting**: Check RBAC permissions and image availability
2. **Secrets not syncing**: Verify MultiKueue CRDs are configured correctly
3. **Connection errors**: Check spoke cluster kubeconfig secrets

### Debug Commands

```bash
# Check controller status
kubectl get deployment secret-controller -n kueue-system

# View detailed logs
kubectl logs -n kueue-system deployment/secret-controller --previous

# Check RBAC
kubectl auth can-i get secrets --as=system:serviceaccount:kueue-system:secret-controller

# List MultiKueue resources
kubectl get multikueueconfigs,multikueueclusters
```

### Log Levels

Set log level via environment variable in deployment:

```yaml
env:
  - name: LOG_LEVEL
    value: "debug" # debug, info, warn, error
```

## Security Considerations

1. **Least Privilege**: The controller only has read access to secrets on the hub cluster
2. **Spoke Cluster Access**: Uses individual kubeconfig secrets per cluster
3. **Network Policies**: Consider restricting network access to spoke clusters
4. **Secret Encryption**: Ensure secrets are encrypted at rest on all clusters

## Monitoring

The controller exposes metrics on port 9090. You can scrape these with Prometheus:

```yaml
# Add to your Prometheus config
- job_name: "secret-controller"
  static_configs:
    - targets: ["secret-controller.kueue-system.svc.cluster.local:9090"]
```

## Uninstalling

```bash
# Remove the controller
kubectl delete -f deploy/deployment.yaml
kubectl delete -f deploy/rbac.yaml

# Optionally remove CRDs
kubectl delete -f examples/multikueue-setup.yaml
```
