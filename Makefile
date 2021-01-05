
build: image manifests/install.yaml manifests/namespace-controller-only.yaml

dist/operator-linux-amd64: GOARGS = GOOS=linux GOARCH=amd64

dist/operator-%: go.mod $(shell find cmd -type f)
	CGO_ENABLED=0 $(GOARGS) go build -v -i -o $@ ./cmd

image: dist/operator-linux-amd64
	docker build . -t argoproj/argo-workflows-operator:latest

/usr/local/bin/kustomize:
	mkdir -p dist
	./hack/recurl.sh dist/install_kustomize.sh https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh
	chmod +x ./dist/install_kustomize.sh
	./dist/install_kustomize.sh 3.8.8
	sudo mv kustomize /usr/local/bin/
	kustomize version

manifests/install.yaml:
manifests/namespace-controller-only.yaml:

.PHONY: manifests/%.yaml
manifests/%.yaml: /usr/local/bin/kustomize
	kustomize build --load_restrictor=none manifests/$* -o manifests/$*.yaml

.PHONY: start
start: manifests/install.yaml image
	k3d image import argoproj/argo-workflows-operator:latest
	kubectl apply -f manifests/install.yaml
