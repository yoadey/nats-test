#!/bin/sh
while true
do
   echo "Restart $(date)"
   kubectl -n nats rollout restart statefulset nats
   kubectl -n nats rollout status  statefulset nats
   sleep 150
done