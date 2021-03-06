package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
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

const (
	managedByLabel = "app.kubernetes.io/managed-by"
	managerName    = "argo-workflows-operator"
	hashLabel      = "argo-workflows-operator.argoproj-labs.io/hash"
	optAnnotation  = "argo-workflows-operator.argoproj-labs.io/opt-in" // maybe `true` or `false`
)

type appDefn struct {
	hash      string
	resources []*unstructured.Unstructured
}

func main() {
	var (
		kubeconfig     string
		scaleUpAfter   time.Duration
		scaleDownAfter time.Duration
		src            string
		optIn          bool
		logLevel       string
	)
	cmd := &cobra.Command{
		Use: "operator",
		Run: func(cmd *cobra.Command, args []string) {
			level, err := log.ParseLevel(logLevel)
			if err != nil {
				log.Fatal(err)
			}
			log.SetLevel(level)

			log.WithFields(log.Fields{
				"src":            src,
				"scaleUpAfter":   scaleUpAfter,
				"scaleDownAfter": scaleDownAfter,
				"gitCommit":      gitCommit,
				"optIn":          optIn,
			}).Info()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sigs := make(chan os.Signal)
			signal.Notify(sigs, os.Interrupt, os.Kill, syscall.SIGTERM)

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
			manifests := "/tmp/manifests.yaml"
			var app appDefn

			syncManifests := func() error {
				err = downloadFile(manifests, src)
				if err != nil {
					return fmt.Errorf("failed to download manifests: %w", err)
				}
				hash, err := hashFile(manifests)
				if err != nil {
					return fmt.Errorf("failed to hash manifests: %w", err)
				}
				if hash == app.hash {
					return nil // un-changed - exit early
				}
				f, err := ioutil.ReadFile(manifests)
				if err != nil {
					return fmt.Errorf("failed to read manifests: %w", err)
				}
				var resources []*unstructured.Unstructured
				for _, part := range strings.Split(string(f), "---") {
					new := &unstructured.Unstructured{}
					err = yaml.Unmarshal([]byte(part), new)
					if err != nil {
						return fmt.Errorf("failed to unmarshall YAML: %ww", err)
					}
					if new.GetLabels() == nil {
						new.SetLabels(map[string]string{})
					}
					labels := new.GetLabels()
					labels[managedByLabel] = managerName // we will not change resource that are not managed
					labels[hashLabel] = hash             // we add the sha1 hash of the resources
					new.SetLabels(labels)
					resources = append(resources, new)
				}
				log.WithFields(log.Fields{"hash": hash, "resources": len(resources)}).Info("manifests synced")
				app = appDefn{hash, resources}
				return nil
			}

			err = syncManifests()
			if err != nil {
				log.Fatal(err)
			}

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
				ns, err := k.CoreV1().Namespaces().Get(namespace, metav1.GetOptions{})
				if err != nil {
					return err
				}
				opt := ns.GetAnnotations()[optAnnotation]
				switch {
				case opt == "true": // ok to proceed
				case opt == "false":
					return nil
				case !optIn:
					return nil
				}
				deploy, err := k.AppsV1().Deployments(namespace).Get("workflow-controller", metav1.GetOptions{})
				switch {
				case apierrors.IsNotFound(err):
				case err != nil:
					return err
				default:
					// is found
					scaledUp := deploy.Spec.Replicas == nil || *deploy.Spec.Replicas >= 1
					oldVersion := deploy.GetLabels()[hashLabel]
					upToDate := oldVersion == app.hash
					logCtx.WithFields(log.Fields{"scaledUp": scaledUp, "upToDate": upToDate, "oldVersion": oldVersion}).Debug()
					if upToDate && scaledUp {
						return nil
					}
				}
				// perform a dry-run first so we reduce the risk of applying some, but not all, of the resources
				logCtx.Info("scaling-up/updating")
				for _, dryRun := range [][]string{{"All"}, nil} {
					for _, resource := range app.resources {
						gvr := gvr(resource)
						key := fmt.Sprintf("%s/%s", gvr.Resource, resource.GetName())
						if len(dryRun) > 0 {
							key = key + " (dry-run)"
						}
						r := dy.Resource(gvr).Namespace(namespace)
						old, err := r.Get(resource.GetName(), metav1.GetOptions{})
						switch {
						case apierrors.IsNotFound(err):
							_, err := r.Create(resource, metav1.CreateOptions{DryRun: dryRun})
							if err != nil {
								return fmt.Errorf("failed to create %v: %w", key, err)
							}
							logCtx.Infof("%v created", key)
							continue
						case err != nil:
							return fmt.Errorf("failed to get %v: %w", key, err)
						}
						if old.GetLabels()[managedByLabel] != managerName {
							logCtx.Warnf("%v un-managed", key)
							continue
						}
						diffs, err := diff(normalize(old), resource)
						if err != nil {
							return fmt.Errorf("failed to diff %v: %w", key, err)
						}
						if diffs == "{}" {
							logCtx.Infof("%v unchanged", key)
							continue
						}
						logCtx.Debug(diffs)
						_, err = r.Patch(resource.GetName(), types.StrategicMergePatchType, []byte(diffs), metav1.PatchOptions{DryRun: dryRun})
						if err != nil {
							return fmt.Errorf("failed to patch %v: %w", key, err)
						}
						logCtx.Infof("%v patched", key)
					}
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
					func(options *metav1.ListOptions) {
						options.LabelSelector = "workflows.argoproj.io/completed!=true" // will be ignored for cronworkflows
					},
				).Informer()

				informer.AddEventHandler(cache.ResourceEventHandlerFuncs{AddFunc: reconcile, DeleteFunc: reconcile})
				informers = append(informers, informer)

				go informer.Run(ctx.Done())
			}

			// start the work-queue
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

			// sync the manifests every 1m
			go func() {
				for range time.Tick(1 * time.Minute) {
					select {
					case <-ctx.Done():
						return
					default:
						err := syncManifests()
						if err != nil {
							log.WithError(err).Error("failed to get manifests")
						}
					}
				}
			}()

			<-sigs
			queue.ShutDown()
			cancel()
			log.Info("done")
		},
	}
	cmd.Flags().StringVar(&kubeconfig, "kubeconfig", func() string {
		if home := homedir.HomeDir(); home != "" {
			return filepath.Join(home, ".kube", "config")
		}
		return ""
	}(), "path to the kubeconfig")
	cmd.Flags().DurationVarP(&scaleUpAfter, "scale-up", "u", 5*time.Second, "scale-up after")
	cmd.Flags().DurationVarP(&scaleDownAfter, "scale-down", "d", 30*time.Second, "scale-down after")
	cmd.Flags().StringVarP(&src, "file", "f", "https://raw.githubusercontent.com/argoproj-labs/argo-workflows-operator/master/manifests/namespace-controller-only.yaml", "manifests to install, https://github.com/hashicorp/go-getter")
	cmd.Flags().BoolVar(&optIn, "opt-in", false, "if namespaces are opted in by default")
	cmd.Flags().StringVar(&logLevel, "loglevel", "info", "log level: error|warning|info|debug")
	err := cmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}

func gvr(new *unstructured.Unstructured) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: new.GroupVersionKind().Group, Version: new.GroupVersionKind().Version, Resource: strings.ToLower(new.GetKind()) + "s"}
}
