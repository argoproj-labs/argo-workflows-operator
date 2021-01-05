package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-getter"
	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/metadata/metadatainformer"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"sigs.k8s.io/yaml"
)

func main() {
	var (
		kubeconfig     string
		scaleUpAfter   time.Duration
		scaleDownAfter time.Duration
		src            string
	)
	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig src")
	} else {
		flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig src")
	}
	flag.DurationVar(&scaleUpAfter, "scale-up-after", 2*time.Second, "scale-up after")
	flag.DurationVar(&scaleDownAfter, "scale-down-after", 5*time.Second, "scale-down after")
	flag.StringVar(&src, "f", "manifests.yaml", "source to install, uses go-getter format")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// connect to the cluster
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatal(err)
		}
	}
	k := kubernetes.NewForConfigOrDie(config)
	dy := dynamic.NewForConfigOrDie(config)
	md := metadata.NewForConfigOrDie(config)

	// get manifests

	manifests := "manifests.yaml"
	err = getter.GetFile(manifests, src)
	if err != nil {
		log.Fatal(err)
	}
	version, err := hashFile(manifests)
	if err != nil {
		log.Fatal(err)
	}

	log.WithFields(log.Fields{"src": src, "version": version}).Info()
	informers := make([]cache.SharedIndexInformer, 0)
	namespaceTouched := sync.Map{} // map[string]*time.Timer
	countResources := func(namespace string) int {
		count := 0
		for _, informer := range informers {
			names, err := informer.GetIndexer().IndexKeys(cache.NamespaceIndex, namespace)
			if err != nil {
				log.Fatal(err)
			}
			count += len(names)
		}
		return count
	}

	reconcile := func(obj interface{}) {
		namespace := obj.(metav1.Object).GetNamespace()
		count := countResources(namespace)
		logCtx := log.WithField("namespace", namespace)
		value, _ := namespaceTouched.Load(namespace)
		timer, ok := value.(*time.Timer)
		if ok {
			timer.Stop()
		}
		if count > 0 {
			scaleUp := func() error {
				deploy, err := k.AppsV1().Deployments(namespace).Get("workflow-controller", metav1.GetOptions{})
				switch {
				case apierrors.IsNotFound(err):
				case err != nil:
					return err
				}
				scaledUp := deploy.Spec.Replicas == nil || *deploy.Spec.Replicas >= 1
				oldVersion := deploy.GetLabels()["app.kubernetes.io/version"]
				upToDate := oldVersion == version
				logCtx.WithFields(log.Fields{"scaledUp": scaledUp, "upToDate": upToDate, "oldVersion": oldVersion}).Info()
				if scaledUp && upToDate {
					logCtx.Info("scaled-up and up-to-date")
					return nil
				}
				logCtx.Info("scaling-up/updating")
				f, err := ioutil.ReadFile(manifests)
				if err != nil {
					return err
				}
				for _, part := range strings.Split(string(f), "---") {
					new := &unstructured.Unstructured{}
					err = yaml.Unmarshal([]byte(part), new)
					if err != nil {
						return err
					}
					if new.GetLabels() == nil {
						new.SetLabels(map[string]string{})
					}
					labels := new.GetLabels()
					labels["app.kubernetes.io/managed-by"] = "argo-workflows-operator"
					labels["app.kubernetes.io/part-of"] = "argo-workflows"
					labels["app.kubernetes.io/version"] = version
					new.SetLabels(labels)
					resource := strings.ToLower(new.GetKind()) + "s"
					gvr := schema.GroupVersionResource{Group: new.GroupVersionKind().Group, Version: new.GroupVersionKind().Version, Resource: resource}
					key := resource + "/" + new.GetName()
					r := dy.Resource(gvr).Namespace(namespace)
					old, err := r.Get(new.GetName(), metav1.GetOptions{})
					switch {
					case apierrors.IsNotFound(err):
						_, err := r.Create(new, metav1.CreateOptions{})
						if err != nil {
							return fmt.Errorf("failed to create %v: %w", key, err)
						}
						logCtx.Infof("%v created", key)
						continue
					case err != nil:
						return fmt.Errorf("failed to get %v: %w", key, err)
					}
					diffs, err := diff(normalize(old), new)
					if err != nil {
						return fmt.Errorf("failed to diff %v: %w", key, err)
					}
					if diffs == "{}" {
						logCtx.Infof("%v unchanged", key)
						continue
					}
					logCtx.Info(diffs)
					_, err = r.Patch(new.GetName(), types.StrategicMergePatchType, []byte(diffs), metav1.PatchOptions{})
					if err != nil {
						return fmt.Errorf("failed to patch %v: %w", key, err)
					}
					logCtx.Infof("%v patched", key)
				}
				return nil
			}
			logCtx.Infof("workflows found: scale-up in %v", scaleUpAfter)
			namespaceTouched.Store(namespace, time.AfterFunc(scaleUpAfter, func() {
				err := scaleUp()
				if err != nil {
					logCtx.WithError(err).Error("failed to scale-up")
				} else {
					logCtx.Info("scaled-up")
				}
			}))
		} else {
			logCtx.Infof("no workflows found: scale-down in %v", scaleDownAfter)
			namespaceTouched.Store(namespace, time.AfterFunc(scaleDownAfter, func() {
				logCtx.Info("scaling-down")
				_, err := k.AppsV1().Deployments(namespace).Patch("workflow-controller", types.MergePatchType, []byte(`{"spec": {"replicas": 0}}`))
				if err != nil {
					logCtx.WithError(err).Error("failed to scale-down")
				} else {
					logCtx.Info("scaled-down")
				}
			}))
		}
	}

	for _, resource := range []string{"workflows", "cronworkflows"} {

		informer := metadatainformer.NewFilteredMetadataInformer(
			md,
			schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: resource},
			corev1.NamespaceAll,
			10*time.Minute,
			cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
			func(options *metav1.ListOptions) {},
		).
			Informer()

		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: reconcile, DeleteFunc: reconcile})
		informers = append(informers, informer)

		go informer.Run(ctx.Done())
	}

	select {}
}
