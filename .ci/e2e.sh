#!/bin/bash

set -exuo pipefail

cd $(dirname $0)

kubectl apply -f ../examples/audit-trace.yaml

set +e
while true
do
    kubectl get svc,ds,po -n default -o wide
    echo "===== audit-trace ====="
    kubectl logs -n default -l app=audit-trace | head
    echo "===== audit-trace ====="
    curl -f http://127.0.0.1:30000/api/traces?service=kubernetes-audit | jq -re .data[].spans && exit 0
    sleep 5
done
