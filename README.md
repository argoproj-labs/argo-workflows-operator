# Argo Workflows Operator

**PROOF OF CONCEPT**

This is not a fully-formed operator (yet).

## Use Case

* You only need the workflow controller, or your Argo Server is managed elsewhere.
* You want to install into many namespaces (for isolation) and have the controller be scaled-to-zero for cost-saving.

## What This Is Not

This is a deployment tool, but is not 

## Summary

This operator is intended to address the problem of installing Argo Workflows into multiple namespaces, but to scale each installation to zero until needed.

When it starts up, it'll get your manifests and save it as `/tmp/manifests.yaml`. 

The operator keeps count of `cronworkflows` and incomplete `workflows`. When the count for a namespace in greater than zero, it waits a short period of time (`scale-up`) and then checks:

* Is there a workflow controller in the namespace which is already scaled up?
* Is it managed by the operator? i.e. labelled `app.kubernetes.io/managed-by=argo-workflows-operator`
* Is it the expected version? i.e. labelled `app.kubernetes.io/version=$(hex $(sha1 /tmp/manifests.yaml))`

If does not exist, is managed, is scaled-down or out of date, then it'll apply the manifests creating the appropriate resources.

## Usage

Install Argo Workflows CRDs:

```bash
kustomize build 'https://github.com/argoproj/argo/manifests/base/crds/minimal?ref=stable' | kubectl apply -f -
```

Install the operator:

```bash
kubectl create ns argo
kubectl -n argo apply -f https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/install.yaml
```

Create a user namespace:

```bash
kubectl create ns my-ns
```

Create the workflow role:

```bash
kubectl -n my-ns apply -f https://raw.githubusercontent.com/argoproj/argo/stable/manifests/quick-start/base/workflow-role.yaml
kubectl -n my-ns apply -f https://raw.githubusercontent.com/argoproj/argo/stable/manifests/quick-start/base/workflow-default-rolebinding.yaml
```

Create a workflow (which will cause a scale-up):

```bash
kubectl -n my-ns create -f https://raw.githubusercontent.com/argoproj/argo/master/examples/hello-world.yaml
kubectl -n my-ns wait wf --for=condition=Completed --all
```

Wait 30s after the workflow finishes, and you'll see it be scale-down.

## Debugging

Tip: you watch for scaling events in the operator logs:

```bash
kubectl -n argo logs deploy/operator --follow
...
level=info resources=6 scaleDownAfter=30s scaleUpAfter=5s src="https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/namespace-controller-only.yaml" version=346705e749ae8df5686f6fdd5c73ac7ec04963f0
...
level=info msg=scaling-up/updating namespace=my-ns
...
time="2021-01-06T00:39:29Z" level=info msg=scaling-down namespace=my-ns
...

```

Scaling results in common Kubernetes events:

```bash
kubectl -n my-ns get events -w --field-selector=involvedObject.kind=Deployment,involvedObject.name=workflow-controller 
...
0s          Normal   ScalingReplicaSet       deployment/workflow-controller              Scaled up replica set workflow-controller-6cc76c86f4 to 1
...
0s          Normal   ScalingReplicaSet       deployment/workflow-controller              Scaled down replica set workflow-controller-6cc76c86f4 to 0
```



## Usage

You can configure the following flags on the operator:

```
Usage:
  operator [flags]

Flags:
  -f, --file string           manifests to install, https://github.com/hashicorp/go-getter (default "git::https://github.com/argoproj-labs/argo-workflows-operator.git//manifests/namespace-controller-only.yaml")
  -h, --help                  help for operator
      --kubeconfig string     path to the kubeconfig (default "/Users/acollins8/.kube/config")
      --loglevel string       log level: error|warning|info|debug (default "info")
  -d, --scale-down duration   scale-down after (default 30s)
  -u, --scale-up duration     scale-up after (default 5s)
```