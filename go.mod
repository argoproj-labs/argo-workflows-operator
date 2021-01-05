module github.com/argoproj-labs/argo-workflow-operator

go 1.13

require (
	cloud.google.com/go v0.55.0 // indirect
	github.com/aws/aws-sdk-go v1.33.16 // indirect
	github.com/evanphx/json-patch v4.5.0+incompatible
	github.com/fsnotify/fsnotify v1.4.9 // indirect
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/golang/protobuf v1.4.2 // indirect
	github.com/google/uuid v1.1.2 // indirect
	github.com/gophercloud/gophercloud v0.7.0 // indirect
	github.com/hashicorp/go-getter v1.5.1
	github.com/hashicorp/golang-lru v0.5.3 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/json-iterator/go v1.1.10 // indirect
	github.com/kr/pretty v0.2.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/onsi/ginkgo v1.11.0 // indirect
	github.com/onsi/gomega v1.8.1 // indirect
	github.com/sirupsen/logrus v1.6.0
	github.com/stretchr/testify v1.6.1 // indirect
	golang.org/x/crypto v0.0.0-20200604202706-70a84ac30bf9 // indirect
	golang.org/x/mod v0.3.1-0.20200828183125-ce943fd02449 // indirect
	golang.org/x/net v0.0.0-20200602114024-627f9648deb9 // indirect
	golang.org/x/tools v0.0.0-20200630154851-b2d8b0336632 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/grpc v1.29.1 // indirect
	k8s.io/api v0.17.8
	k8s.io/apimachinery v0.17.8
	k8s.io/client-go v0.17.8
	k8s.io/utils v0.0.0-20200327001022-6496210b90e8
	sigs.k8s.io/yaml v1.2.0
)

replace (
	github.com/grpc-ecosystem/grpc-gateway => github.com/grpc-ecosystem/grpc-gateway v1.12.2
	sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.2.9
)
