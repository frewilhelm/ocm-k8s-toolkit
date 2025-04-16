# Setup
kind create cluster

flux install

helm install kro oci://ghcr.io/kro-run/kro/kro --namespace kro --create-namespace --version=0.2.2

# OCM
ocm add cv --create --file ./demo/ctf ./demo/component-constructor.yaml
# IMPORTANT!!! Adjust OCM repository in bootstrap
ocm transfer ctf --copy-resources --overwrite ./demo/ctf ghcr.io/frewilhelm

# Start controllers
IMG=ghcr.io/frewilhelm/ocm-controllers make deploy

# Deploy resources
kubectl apply -f demo/bootstrap.yaml

# Wait before deploying instance as the rgd CRD must be created first
kubectl wait rgd/demo-rgd --for=create --timeout=1m
kubectl wait rgd/demo-rgd --for=condition=ReconcilerReady=true --timeout=1m

kubectl apply -f demo/instance.yaml