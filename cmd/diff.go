package main

import (
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func diff(old *unstructured.Unstructured, new *unstructured.Unstructured) (string, error) {
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(new)
	patch, err := jsonpatch.CreateMergePatch(a, b)
	return string(patch), err
}
