# Minikube Deployment Guide

This guide describes how to run the peep platform locally on Minikube using the new Kubernetes manifests under `k8s/base`.

## Prerequisites

- macOS or Linux with Docker installed
- [Minikube](https://minikube.sigs.k8s.io/docs/start/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kustomize](https://kubectl.docs.kubernetes.io/installation/kustomize/) (optional, bundled with newer kubectl)
- mkcert or openssl for generating a local TLS certificate (`peep.com`)

## 1. Start Minikube

```bash
minikube start --cpus=4 --memory=8g --disk-size=40g
```

## 2. Use the Minikube Docker Daemon

Build images inside the Minikube Docker environment so Kubernetes can pull them without pushing to a registry:

```bash
eval "$(minikube docker-env)"
```

Build images for each service:

```bash

# API
docker build -t peep/api:dev -f api/Dockerfile .

# Builder
docker build -t peep/builder:dev -f builder/Dockerfile .

# Dashboard
npm --prefix dashboard install
npm --prefix dashboard run build
docker build -t peep/dashboard:dev -f dashboard/Dockerfile .
```

> Ensure each Dockerfile uses a production entrypoint (no `go run` or `npm run dev`).

## 3. Generate TLS Secret

Create a certificate for `peep.com` (adjust paths as needed):

```bash
mkcert -install
mkcert peep.com
kubectl create secret tls peep-tls \
  --namespace peep \
  --cert=peep.com.pem \
  --key=peep.com-key.pem
```

Alternatively use openssl:

```bash
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -out peep.com.crt \
  -keyout peep.com.key \
  -subj "/CN=peep.com/O=peep"

kubectl create secret tls peep-tls \
  --namespace peep \
  --cert=peep.com.crt \
  --key=peep.com.key
```

## 4. Apply Base Manifests

```bash
kubectl apply -k k8s/base
```

This creates:

- `peep` namespace
- PostgreSQL StatefulSet (5Gi PVC)
- Redis Deployment
- API, Builder, Dashboard Deployments + Services
- Edge Nginx Deployment + Service
- Ingress for `peep.com`

## 5. Verify Pods

```bash
kubectl get pods -n peep
```

All pods should reach `Running` with ready containers. Inspect logs if any pod restarts:

```bash
kubectl logs -n peep deploy/api
kubectl logs -n peep deploy/builder
kubectl logs -n peep deploy/dashboard
```

## 6. Update /etc/hosts

Add an entry for the ingress host:

```
127.0.0.1 peep.com
```

For Minikube’s built-in ingress addon:

```bash
minikube addons enable ingress
minikube tunnel
```

If you use the bundled `edge-nginx` deployment instead, expose it via NodePort and port-forward:

```bash
kubectl port-forward -n peep svc/edge-nginx 8080:80
```

Then visit https://peep.com (accept the self-signed certificate).

## 7. Database Migrations

Run migrations inside the cluster:

```bash
kubectl run migrate --rm -it \
  --namespace peep \
  --image=peep/api:dev \
  --restart=Never \
  --env-from=secret/api-env \
  --command -- goose postgres "$DATABASE_URL" up
```

Ensure the `goose` binary is available in the API image, or ship a dedicated migration image/job.

## 8. Redeploying Images

When rebuilding images, delete the pods to pull the updated tag:

```bash
kubectl rollout restart deployment/api deployment/builder deployment/dashboard -n peep
```

## 9. Troubleshooting

- Check `kubectl describe` for failing pods.
- Ensure the TLS secret `peep-tls` exists before applying the ingress.
- Monitor resource usage with `kubectl top pods -n peep` and tune requests/limits as needed.
- Keep secrets in Kubernetes (`kubectl edit secret ...`) rather than committing production values.

## 10. Cleaning Up

```bash
kubectl delete namespace peep
minikube delete
```

## Next Steps

- Create overlays for staging/production with environment-specific secrets.
- Automate image builds via CI/CD pushing to a registry (e.g., GHCR) and use `imagePullSecrets`.
- Replace Docker socket usage in the builder with Kubernetes-native build mechanisms (BuildKit, Kaniko, or Tekton).
