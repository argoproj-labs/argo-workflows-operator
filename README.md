# Argo Workflows Operator

**PROOF OF CONCEPT**

## Summary

This operator is intended to address the problem of installing Argo Workflows into multiple namespaces, but to scale each installation to zero until needed.

When it starts up, it'll get the source manifests and save it as `manifests.yaml`. 

The operator listens to `workflows` and `cronworkflows` in all namespaces. When one of these comes into existence in a namespace, it waits a short period of time (`scale-up-duration`) and then checks:

* Is there a workflow controller in the namespace already scaled up?
* Is it managed by the operator? i.e. labelled `app.kubernetes.io/managed-by=argo-workflows-operator`
* Is it the correct version? i.e. labelled `app.kubernetes.io/version=$(hex $(sha1 manifests.yaml))`

If does not exist, is managed, is scaled-down or out of date, then it'll update the workflow controller.

## Usage

Install Argo Workflows CRDs:

```bash
kustomize build https://github.com/argoproj/argo/manifests/base/crds/minimal\?ref\=stable | kubectl apply -f -
```

Install the operator:

```bash
kubectl create ns argo
kubectl -n argo apply -f https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/install.yaml
```

Tip: you check the behaviour in the operator logs:

```bash
kubectl -n argo logs deploy/operator --follow
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

Submit a workflow (which will cause a scale-up):

```bash
argo submit -n my-ns --watch https://raw.githubusercontent.com/argoproj/argo/master/examples/hello-world.yaml
```

Delete all workflows (which will cause a scale-down):

```bash
kubectl -n my-ns delete wf --all
```

## Options

```
  -f string
        source to install, uses go-getter format (default "https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/namespace-controller-only.yaml")
  -kubeconfig string
        (optional) absolute path to the kubeconfig src (default "/Users/acollins8/.kube/config")
  -loglevel string
        log level: info|debug (default "debug")
  -scale-down-after duration
        scale-down after (default 30s)
  -scale-up-after duration
        scale-up after (default 10s)
```