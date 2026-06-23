# vpa-provisioner

Lightweight Kubernetes controller that automatically provisions a
[VerticalPodAutoscaler (VPA)](https://github.com/kubernetes/autoscaler/tree/master/vertical-pod-autoscaler)
for every Deployment and StatefulSet in the cluster.

VPAs are created with `updatePolicy.updateMode: "Off"` so the VPA Recommender
can collect rightsizing recommendations **without** modifying live pod resources or
forcing restarts.

## Prerequisites

vpa-provisioner is designed for a **recommender-only** VPA install. The controller
validates these requirements at startup (unless `-skip-prerequisite-check` is set).

### Required (startup fails if missing)

| Requirement | Why | How to verify |
|-------------|-----|---------------|
| **VPA CRD** (`verticalpodautoscalers.autoscaling.k8s.io`) | Controller creates `VerticalPodAutoscaler` objects | `kubectl get crd verticalpodautoscalers.autoscaling.k8s.io` |
| **VPA API** (`autoscaling.k8s.io/v1`) | API must be registered on the apiserver | `kubectl api-resources \| grep verticalpodautoscaler` |
| **Controller RBAC** | Service account must manage VPAs and watch workloads | See [deploy/rbac/rbac.yaml](deploy/rbac/rbac.yaml) |

Install the CRD from the upstream VPA release:

```bash
kubectl apply -f https://github.com/kubernetes/autoscaler/releases/latest/download/vpa-crd.yaml
```

### Expected (warns if missing; use `-require-recommender` to fail)

| Requirement | Why | How to verify |
|-------------|-----|---------------|
| **VPA Recommender** deployment (`vpa-recommender`) | Generates CPU/memory recommendations consumed by VPAs | `kubectl -n kube-system get deploy vpa-recommender` |

Only the recommender is needed. Without it, VPAs are created but produce no recommendations.

### Must NOT be installed (warns if detected)

| Component | Why |
|-----------|-----|
| **VPA Admission Controller** (`vpa-admission-controller`) | Mutates pod resources at admission time |
| **VPA Updater** (`vpa-updater`) | Evicts pods to apply new resource requests |
| **VPA mutating webhooks** | Indicates admission control may be active |

This project uses `updateMode: "Off"` specifically to avoid live resource changes.
Install recommender only:

```bash
# Example: official manifests — install ONLY the recommender components
# Do not apply admission-controller or updater manifests
kubectl apply -f https://github.com/kubernetes/autoscaler/releases/latest/download/vpa-rbac.yaml
kubectl apply -f https://github.com/kubernetes/autoscaler/releases/latest/download/vpa-recommender.yaml
```

### Startup validation flags

| Flag | Default | Description |
|------|---------|-------------|
| `-skip-prerequisite-check` | `false` | Skip all prerequisite checks (local dev only) |
| `-require-recommender` | `false` | Fail startup if `vpa-recommender` is not found |

At startup the controller checks:

1. VPA API/CRD is registered on the apiserver
2. Service account can `list` `verticalpodautoscalers`
3. Whether `vpa-recommender`, `vpa-admission-controller`, or `vpa-updater` exist in common namespaces
4. Whether any `MutatingWebhookConfiguration` with `vpa` in the name exists (advisory)

## Design

| Decision | Rationale |
|----------|-----------|
| Background controller (not webhook) | Failure domain isolation — if the controller is down, workloads are unaffected |
| `updateMode: "Off"` | Recommendations only; no live resource mutation |
| `ownerReferences` on VPAs | Garbage collection when parent workload is deleted |
| Managed label + reconcile GC | Deletes provisioner-owned VPAs when workloads are excluded or removed; never touches foreign VPAs |
| Unstructured dynamic client | No VPA API dependency in `go.mod` |
| Opt-out label | `vpa-provisioner/skip: "true"` skips provisioning |
| Namespace guardrails | `kube-system` excluded by default (configurable) |

## Container image

Images are published to **`ghcr.io/runwhen-contrib/vpa-provisioner`**.

| Tag | When |
|-----|------|
| `main` | Latest main build |
| `main-<sha>` | Immutable commit tag on main |
| `pr-<n>` | Latest build for pull request *n* (moving pointer) |
| `pr-<n>-<sha>` | Immutable commit tag for a PR revision |

Workflows:

- **Build and Push** (`.github/workflows/build-and-push.yaml`) — runs on push to `main`
- **PR Image** (`.github/workflows/pr-image.yaml`) — runs on pull requests; pushes preview tags and comments on the PR with pull/install commands

## Quick start

### Helm

```bash
helm install vpa-provisioner ./charts/vpa-provisioner \
  --namespace vpa-provisioner \
  --create-namespace \
  --set image.tag=main
```

### Plain manifests

```bash
kubectl apply -f deploy/manifests/namespace.yaml
kubectl apply -f deploy/manifests/configmap.yaml
kubectl apply -f deploy/rbac/rbac.yaml
kubectl apply -f deploy/rbac/role-configmap.yaml
kubectl apply -f deploy/manifests/deployment.yaml
```

## Opt out

Workloads can be excluded at three levels. All checks are evaluated on every
sync; the ConfigMap is hot-reloaded when edited.

### 1. Per-workload label or annotation

Set on the Deployment or StatefulSet metadata:

```yaml
metadata:
  labels:
    vpa-provisioner/skip: "true"
```

Or use the same key as an annotation:

```yaml
metadata:
  annotations:
    vpa-provisioner/skip: "true"
```

### 2. CLI / Helm namespace list

Default excluded namespaces via `-exclude-namespaces` or Helm `excludeNamespaces`.

### 3. Exclusion ConfigMap

The controller watches `vpa-provisioner-config` (configurable) for explicit
namespace and workload omissions:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: vpa-provisioner-config
  namespace: vpa-provisioner
data:
  config.yaml: |
    excludeNamespaces:
      - monitoring
      - cert-manager
    excludeWorkloads:
      - namespace: backend-services
        kind: Deployment
        name: papi
      - namespace: data
        kind: StatefulSet
        name: postgres
```

Helm values:

```yaml
exclusionConfig:
  enabled: true
  excludeNamespaces:
    - monitoring
  excludeWorkloads:
    - namespace: backend-services
      kind: Deployment
      name: papi
```

CLI flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-config-map-name` | `vpa-provisioner-config` | Exclusion ConfigMap name (empty disables watch) |
| `-config-map-namespace` | `$POD_NAMESPACE` or `vpa-provisioner` | ConfigMap namespace |

## Generated VPA shape

For a Deployment named `nginx` in namespace `default`, the controller creates
`default/nginx-deployment-vpa`:

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: nginx-deployment-vpa
  namespace: default
  ownerReferences:
    - apiVersion: apps/v1
      kind: Deployment
      name: nginx
      uid: <deployment-uid>
      controller: true
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: nginx
  updatePolicy:
    updateMode: "Off"
```

## Development

```bash
make build   # bin/vpa-provisioner
make test
make docker  # ghcr.io/runwhen-contrib/vpa-provisioner:dev
```

Run locally against a cluster:

```bash
go run ./cmd/vpa-provisioner -exclude-namespaces=kube-system
```

Skip checks for local development without VPA installed:

```bash
go run ./cmd/vpa-provisioner -skip-prerequisite-check
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
