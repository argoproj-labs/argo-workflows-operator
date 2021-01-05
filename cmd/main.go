package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
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
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/yaml"
)

func main() {
	var (
		kubeconfig     string
		scaleUpAfter   time.Duration
		scaleDownAfter time.Duration
		src            string
		logLevel       string
	)
	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig src")
	} else {
		flag.StringVar(&kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig src")
	}
	flag.DurationVar(&scaleUpAfter, "scale-up-after", 5*time.Second, "scale-up after")
	flag.DurationVar(&scaleDownAfter, "scale-down-after", 30*time.Second, "scale-down after")
	flag.StringVar(&src, "f", "https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/namespace-controller-only.yaml", "source to install, uses go-getter format")
	flag.StringVar(&logLevel, "loglevel", "debug", "log level: info|debug")
	flag.Parse()

	level, err := log.ParseLevel(logLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(level)

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

	log.WithFields(log.Fields{"src": src, "version": version, "scaleUpAfter": scaleUpAfter, "scaleDownAfter": scaleDownAfter}).Info()
	informers := make([]cache.SharedIndexInformer, 0)
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
	scaleUp := func(namespace string) error {
		logCtx := log.WithField("namespace", namespace)
		deploy, err := k.AppsV1().Deployments(namespace).Get("workflow-controller", metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return err
		default:
			scaledUp := deploy.Spec.Replicas == nil || *deploy.Spec.Replicas >= 1
			oldVersion := deploy.GetLabels()["app.kubernetes.io/version"]
			managed := deploy.GetLabels()["app.kubernetes.io/managed-by"] == "argo-workflows-operator"
			upToDate := oldVersion == version
			logCtx.WithFields(log.Fields{"scaledUp": scaledUp, "upToDate": upToDate, "managed": managed, "oldVersion": oldVersion}).Debug()
			if upToDate && scaledUp || !managed {
				return nil
			}
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
	scaleDown := func(namespace string) error {
		logCtx := log.WithField("namespace", namespace)
		logCtx.Info("scaling-down")
		_, err := k.AppsV1().Deployments(namespace).Patch("workflow-controller", types.MergePatchType, []byte(`{"spec": {"replicas": 0}}`))
		return err
	}

	queue := workqueue.NewDelayingQueue()
	reconcile := func(obj interface{}) {
		namespace := obj.(metav1.Object).GetNamespace()
		logCtx := log.WithField("namespace", namespace)
		count := countResources(namespace)
		if count > 0 {
			logCtx.Debugf("resources found: scale-up in %v", scaleUpAfter)
			queue.AddAfter(namespace, scaleUpAfter)
		} else {
			logCtx.Debugf("no resources found: scale-down in %v", scaleDownAfter)
			queue.AddAfter(namespace, scaleDownAfter)
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

	go func() {
		for {
			key, shutdown := queue.Get()
			if shutdown {
				return
			}
			namespace := key.(string)
			logCtx := log.WithField("namespace", namespace)
			err := func() error {
				defer queue.Done(key)
				count := countResources(namespace)
				if count > 0 {
					return scaleUp(namespace)
				} else {
					return scaleDown(namespace)
				}
			}()
			if err != nil {
				logCtx.WithError(err).Error("failed to scale-up/down")
			}
		}
	}()

	<-ctx.Done()
	queue.ShutDown()
	log.Info("done")
}
