
build: image manifests/install.yaml manifests/namespace-controller-only.yaml

dist/operator-linux-amd64: GOARGS = GOOS=linux GOARCH=amd64

dist/operator-%: go.mod $(shell find cmd -type f)
	CGO_ENABLED=0 $(GOARGS) go build -v -i -ldflags='-s -w -X github.com/argoproj-labs/argo-workflows-operator/cmd.gitCommit=$(shell git rev-parse HEAD)' -o $@ ./cmd

image: dist/operator-linux-amd64
	docker build . -t argoproj/argo-workflows-operator:latest

manifests/install.yaml:
manifests/namespace-controller-only.yaml:

manifests/%.yaml:
	kustomize build --load_restrictor=none manifests/$* -o manifests/$*.yaml

start: manifests/install.yaml manifests/namespace-controller-only.yaml image
	k3d cluster get || k3d cluster create --no-lb --no-hostip --switch-context
	kubectl config set-context --current --namespace=argo
	kustomize build 'https://github.com/argoproj/argo/manifests/base/crds/minimal?ref=stable' | kubectl apply -f -
	kubectl get ns argo || kubectl create ns argo
	k3d image import argoproj/argo-workflows-operator:latest
	kubectl -n argo apply -f manifests/install.yaml
	kubectl -n argo rollout restart deploy/operator

test: start
	kubectl get ns my-ns || kubectl create ns my-ns
	kubectl -n my-ns apply -f https://raw.githubusercontent.com/argoproj/argo/stable/manifests/quick-start/base/workflow-role.yaml
	kubectl -n my-ns apply -f https://raw.githubusercontent.com/argoproj/argo/stable/manifests/quick-start/base/workflow-default-rolebinding.yaml
	kubectl -n my-ns delete wf --all
	kubectl -n my-ns create -f https://raw.githubusercontent.com/argoproj/argo/master/examples/hello-world.yaml
	kubectl -n my-ns wait wf --for=condition=Completed --all
	kubectl -n argo logs deploy/operator --follow

lint:
	go mod tidy
	go clean
	goimports -w ./cmd
