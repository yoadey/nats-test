# Tool to test the data in a NATS instance during restarts


## Setup

1. Adapt the persistence class in your nats-values.yaml
2. Deploy NATS to the cluster in the NATS namespace
```
helm repo add nats https://nats-io.github.io/k8s/helm/charts/
helm --kube-context <context> --namespace nats --install --create-namespace upgrade nats nats/nats --version 1.1.12 -f nats-values.yaml
```
3. If there were already kvstores, you may need to delete them, they are automatically recreated:
```
nats kv rm <kvstorename> -f
```
4. Deploy the nats-test by applying the `deploy.yml` file:
```kubectl --context <context> apply -f deploy.yml```
5. Scale the nats-test deployment and the restart-nats-statefulset to 1
6. Monitor the output of the nats-test pod, as soon as it is out of sync, it will show the Message "KEY NOT FOUND: ..."
7. Stop the test by scaling the nats-test deployment and the restart-nats-statefulset to 0
8. Check issue by opening the nats-box and using the following command multiple times
```
nats kv ls testkv --trace
```
9. Output should sometimes be `Key1` and sometimes `No keys found in bucket`