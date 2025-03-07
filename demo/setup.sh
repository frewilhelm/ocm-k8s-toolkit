#!/bin/sh

# Create kind cluster with config
kind create cluster --config config.yaml

# Deploy internal image registry
kubectl apply -f registry.yaml
kubectl wait pod -l app=registry --for condition=Ready --timeout 1m

docker pull ghcr.io/stefanprodan/podinfo:6.7.1
docker tag ghcr.io/stefanprodan/podinfo:6.7.1 localhost:31000/stefanprodan/podinfo:6.7.1
docker push localhost:31000/stefanprodan/podinfo:6.7.1

ocm add cv --create --file ./ctf  cc.yaml
ocm transfer ctf --overwrite ./ctf http://localhost:31000

flux install

pushd ..
export IMG=localhost:31000/ocm-controllers
make docker-build && make docker-push && make deploy-dev
popd

kubectl apply -f resources.yaml
kubectl apply -f ocirepository.yaml
