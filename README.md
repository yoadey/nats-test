# Tool to test the data in a NATS instance during restarts


## Setup

1. Deploy NATS to the cluster in the NATS namespace
1. Execute the following commands in nats-box to create the KV store:
```
nats kv rm testkv -f
nats kv add --replicas=3 --storage=file testkv
```
1. Deploy the nats-test by applying the `deploy.yml` file:
```kubectl apply -f deploy.yml```
1. Run the `stsrestart.sh` to restart the nats container every 150s
1. Monitor the output of the nats-test pod, as soon as it is out of sync, it will show a message