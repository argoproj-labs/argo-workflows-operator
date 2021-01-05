package main

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func normalize(old *unstructured.Unstructured) *unstructured.Unstructured {
	new := old.DeepCopy()
	delete(new.Object, "metadata")
	delete(new.Object, "status")
	delete(new.Object, "secrets")
	switch new.GetAPIVersion() + "/" + new.GetKind() {
	case "v1/Service":
		spec := new.Object["spec"].(map[string]interface{})
		delete(spec, "clusterIP")
		if spec["sessionAffinity"] == "None" {
			delete(spec, "sessionAffinity")
		}
		if spec["type"] == "ClusterIP" {
			delete(spec, "type")
		}
	}
	new.SetName(old.GetName())
	if old.GetAnnotations() != nil {
		annotations := old.GetAnnotations()
		for k := range annotations {
			if strings.Contains(k, ".kubernetes.io/") {
				delete(annotations, k)
			}
		}
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		delete(annotations, "deployment.kubernetes.io/revision")
		if len(annotations) > 0 {
			new.SetAnnotations(annotations)
		}
	}
	if old.GetLabels() != nil {
		new.SetLabels(old.GetLabels())
	}
	return new
}
