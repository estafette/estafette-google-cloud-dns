# estafette-google-cloud-dns

This small Kubernetes application configures dns in Google Cloud DNS for any service or ingress with the correct annotations

[![License](https://img.shields.io/github/license/estafette/estafette-google-cloud-dns.svg)](https://github.com/estafette/estafette-google-cloud-dns/blob/master/LICENSE)

## Why?

In order not to have to set dns records manually or from deployment scripts this application decouples that responsibility and moves it into the Kubernetes cluster itself.

## Usage

Deploy:

```
export TEAM_NAME=tooling
export VERSION=0.0.1
export GO_PIPELINE_LABEL=0.0.1
export GOOGLE_CLOUD_DNS_PROJECT="my-gcp-project-name"
export GOOGLE_CLOUD_DNS_ZONE="my-cloud-dns-zone-name

# Setup RBAC
curl https://raw.githubusercontent.com/estafette/estafette-google-cloud-dns/master/rbac.yaml | kubectl apply -f -

# Install application
curl https://raw.githubusercontent.com/estafette/estafette-google-cloud-dns/master/kubernetes.yaml | envsubst | kubectl apply -f -
```

Award the following role to the automatically generated service account in the project specified by GOOGLE_CLOUD_DNS_PROJECT:

* DNS Administrator

Once it's running put the following annotations on a service of type LoadBalancer and deploy. The estafette-goole-cloud-dns application will watch changes to services and process those. Once approximately every 300 seconds it also scans all services as a safety net.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: myapplication
  namespace: mynamespace
  labels:
    app: myapplication
  annotations:
    estafette.io/google-cloud-dns: "true"
    estafette.io/google-cloud-dns-hostnames: "mynamespace.mydomain.com"
spec:
  type: LoadBalancer
  ports:
  - name: http
    port: 80
    targetPort: http
    protocol: TCP
  selector:
    app: myapplication
```