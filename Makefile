
build: image manifests/install.yaml manifests/namespace-controller-only.yaml

dist/operator-linux-amd64: GOARGS = GOOS=linux GOARCH=amd64

dist/operator-%: go.mod $(shell find cmd -type f)
	CGO_ENABLED=0 $(GOARGS) go build -v -i -o $@ ./cmd

image: dist/operator-linux-amd64
	docker build . -t argoproj/argo-workflows-operator:latest


manifests/install.yaml:
manifests/namespace-controller-only.yaml:

manifests/%.yaml:
	kustomize build --load_restrictor=none manifests/$* -o manifests/$*.yaml

start: manifests/install.yaml manifests/namespace-controller-only.yaml image
	k3d image import argoproj/argo-workflows-operator:latest
	kubectl -n argo apply -f manifests/install.yaml
	kubectl -n argo rollout restart deploy/operator
	sleep 10s
	kubectl -n argo logs deploy/operator --follow