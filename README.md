# estafette-google-cloud-dns

This small Kubernetes application configures dns in Google Cloud DNS for any service or ingress with the correct annotations

[![License](https://img.shields.io/github/license/estafette/estafette-google-cloud-dns.svg)](https://github.com/estafette/estafette-google-cloud-dns/blob/master/LICENSE)

## Why?

In order not to have to set dns records manually or from deployment scripts this application decouples that responsibility and moves it into the Kubernetes cluster itself.

## Installation

Prepare using Helm:

```
brew install kubernetes-helm
kubectl -n kube-system create serviceaccount tiller
kubectl create clusterrolebinding tiller --clusterrole=cluster-admin --serviceaccount=kube-system:tiller
helm init --service-account tiller --wait
```

Then install or upgrade with Helm:

```
helm repo add estafette https://helm.estafette.io
helm upgrade --install estafette-google-cloud-dns --namespace estafette estafette/estafette-google-cloud-dns
```

## IAM

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