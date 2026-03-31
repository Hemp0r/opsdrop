# OpsDrop Helm Chart

Deploy OpsDrop on Kubernetes using the official Helm chart hosted as an OCI
artifact on GitHub Container Registry.

---

## Prerequisites

- Kubernetes 1.26+
- Helm 3.12+
- A storage class that supports `ReadWriteOnce` (for SQLite persistence)

---

## Quick Start

```bash
# 1. Generate a JWT signing secret
JWT_SECRET=$(openssl rand -hex 32)

# 2. Install the chart
helm install opsdrop oci://ghcr.io/hemp0r/opsdrop/opsdrop \
  --namespace opsdrop --create-namespace \
  --set config.jwtSecret="${JWT_SECRET}"
```

The pod starts on port 8080 (HTTP). Use port-forwarding to verify:

```bash
kubectl port-forward svc/opsdrop 8080:8080 -n opsdrop
curl http://localhost:8080/healthz
```

---

## Configuration

All configuration is done through Helm values. Override defaults with
`--set key=value` or a custom `values.yaml` file.

### Application Settings

| Value | Description | Default |
|---|---|---|
| `config.jwtSecret` | JWT signing key (≥ 32 chars). Ignored when `existingSecret` is set. Generate with `openssl rand -hex 32`. | `""` |
| `existingSecret` | Name of an existing Kubernetes Secret containing a `jwt-secret` key. Takes precedence over `config.jwtSecret`. | `""` |
| `config.registrationEnabled` | Allow public user registration. | `true` |
| `config.maxUploadSizeBytes` | Maximum upload size in bytes. `0` = unlimited. | `0` |

### Image

| Value | Description | Default |
|---|---|---|
| `image.repository` | Container image repository. | `ghcr.io/hemp0r/opsdrop` |
| `image.tag` | Image tag. | `stable` |
| `image.pullPolicy` | Pull policy. | `IfNotPresent` |
| `imagePullSecrets` | Registry pull secrets. | `[]` |

### Networking

| Value | Description | Default |
|---|---|---|
| `service.type` | Service type (`ClusterIP`, `NodePort`, `LoadBalancer`). | `ClusterIP` |
| `service.port` | Service port. | `8080` |
| `ingress.enabled` | Create an Ingress resource. | `false` |
| `ingress.className` | Ingress class name (e.g. `nginx`, `traefik`). | `""` |
| `ingress.annotations` | Ingress annotations (see examples below). | `{}` |
| `ingress.hosts` | List of `{host, paths}` entries. | `[{host: opsdrop.example.com, paths: [{path: /, pathType: Prefix}]}]` |
| `ingress.tls` | TLS configuration. | `[]` |
| `networkPolicy.enabled` | Deploy a NetworkPolicy (deny-all with HTTP + DNS exceptions). | `true` |
| `networkPolicy.additionalIngress` | Extra ingress rules (e.g. monitoring). | `[]` |
| `networkPolicy.additionalEgress` | Extra egress rules. | `[]` |

### Persistence

| Value | Description | Default |
|---|---|---|
| `persistence.enabled` | Enable persistent storage (strongly recommended). | `true` |
| `persistence.storageClass` | Storage class. Empty uses the cluster default. | `""` |
| `persistence.accessModes` | PVC access modes. | `[ReadWriteOnce]` |
| `persistence.size` | PVC size. | `10Gi` |
| `persistence.existingClaim` | Use an existing PVC instead of creating one. | `""` |

### Resources & Scheduling

| Value | Description | Default |
|---|---|---|
| `replicaCount` | Number of replicas. Must be **1** (SQLite single-writer). | `1` |
| `resources.requests.cpu` | CPU request. | `100m` |
| `resources.requests.memory` | Memory request. | `128Mi` |
| `resources.limits.cpu` | CPU limit. | `500m` |
| `resources.limits.memory` | Memory limit. | `256Mi` |
| `nodeSelector` | Node selector labels. | `{}` |
| `tolerations` | Pod tolerations. | `[]` |
| `affinity` | Pod affinity rules. | `{}` |

### Service Account

| Value | Description | Default |
|---|---|---|
| `serviceAccount.create` | Create a ServiceAccount. | `true` |
| `serviceAccount.name` | Override the ServiceAccount name. | `""` |
| `serviceAccount.annotations` | ServiceAccount annotations (e.g. for IRSA/Workload Identity). | `{}` |

---

## Examples

### Minimal install with Ingress and Let's Encrypt

```yaml
# values-prod.yaml
config:
  jwtSecret: ""           # use existingSecret instead
  registrationEnabled: false
  maxUploadSizeBytes: 1073741824   # 1 GiB

existingSecret: opsdrop-jwt

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "1024m"
  hosts:
    - host: opsdrop.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: opsdrop-tls
      hosts:
        - opsdrop.example.com

persistence:
  size: 50Gi
```

Create the JWT secret first, then install:

```bash
kubectl create namespace opsdrop

kubectl create secret generic opsdrop-jwt \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" \
  -n opsdrop

helm install opsdrop oci://ghcr.io/hemp0r/opsdrop/opsdrop \
  -n opsdrop -f values-prod.yaml
```

### Traefik with IngressRoute (annotations-only)

```bash
helm install opsdrop oci://ghcr.io/hemp0r/opsdrop/opsdrop \
  -n opsdrop --create-namespace \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set ingress.enabled=true \
  --set ingress.className=traefik \
  --set ingress.hosts[0].host=opsdrop.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

### Use an existing PVC

```bash
helm install opsdrop oci://ghcr.io/hemp0r/opsdrop/opsdrop \
  -n opsdrop --create-namespace \
  --set config.jwtSecret="$(openssl rand -hex 32)" \
  --set persistence.existingClaim=my-existing-pvc
```

---

## Upgrading

```bash
helm upgrade opsdrop oci://ghcr.io/hemp0r/opsdrop/opsdrop \
  -n opsdrop -f values-prod.yaml
```

The Deployment uses `strategy: Recreate` because SQLite requires a single
writer. During upgrades there will be a brief downtime while the new pod starts.

---

## Uninstalling

```bash
helm uninstall opsdrop -n opsdrop
```

> **Note:** The PersistentVolumeClaim is **not** deleted by `helm uninstall`.
> This is intentional to prevent data loss. Delete it manually if you want to
> remove all data:
>
> ```bash
> kubectl delete pvc opsdrop -n opsdrop
> ```

---

## Security Notes

- TLS is **disabled** inside the pod (`SERVER_TLS_ENABLED=false`). Terminate
  TLS at the Ingress controller or a reverse proxy in front of the Service.
- The pod runs as non-root (UID 1000) with a read-only root filesystem, dropped
  capabilities, and seccomp `RuntimeDefault`.
- The NetworkPolicy (enabled by default) restricts ingress to port 8080 and
  egress to DNS only. Add rules via `networkPolicy.additionalIngress` /
  `networkPolicy.additionalEgress` if the pod needs to reach external services.
- The `jwt-secret` is mounted from a Kubernetes Secret — never store it in
  plain-text `values.yaml` files committed to version control. Use
  `existingSecret` or a secrets manager in production.

See [HOSTING.md](HOSTING.md) for reverse-proxy hardening guidance (HSTS, rate
limiting, request size limits, trusted proxies).

---

## Chart Release Process

The chart is automatically packaged and pushed to
`oci://ghcr.io/hemp0r/opsdrop/opsdrop` on every push to `main` that modifies
files under `chart/`. The version is controlled by `Chart.yaml`.
