# Tool to test the data in a NATS instance during restarts


## Setup

1. Adapt the persistence class in your nats-values.yaml
1. Deploy NATS to the cluster in the NATS namespace
```
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm --kube-context <context> --namespace nats --install --create-namespace upgrade nats nats/nats --version 1.1.6 -f nats-values.yaml
```
1. Execute the following commands in nats-box to create the KV store:
```
nats kv rm testkv -f
nats kv add --replicas=3 --storage=file testkv
```
1. Deploy the nats-test by applying the `deploy.yml` file:
```kubectl --context <context> apply -f deploy.yml```
1. Monitor the output of the nats-test pod, as soon as it is out of sync, it will show a message