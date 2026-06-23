// Copyright 2026 RunWhen
// SPDX-License-Identifier: Apache-2.0

package exclusion

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// ConfigMapWatcher loads and hot-reloads exclusion policy from a ConfigMap.
type ConfigMapWatcher struct {
	clientset  kubernetes.Interface
	namespace  string
	name       string
	basePolicy Policy
	policy     atomic.Value
	onChange   func()
	logger     *slog.Logger
}

func NewConfigMapWatcher(clientset kubernetes.Interface, namespace, name string, basePolicy Policy) *ConfigMapWatcher {
	w := &ConfigMapWatcher{
		clientset:  clientset,
		namespace:  namespace,
		name:       name,
		basePolicy: basePolicy,
		logger:     slog.Default().With("component", "exclusion-config"),
	}
	w.storePolicy(basePolicy, false)
	return w
}

func (w *ConfigMapWatcher) Current() Policy {
	if value := w.policy.Load(); value != nil {
		return value.(Policy)
	}
	return w.basePolicy
}

// OnPolicyChange registers a callback invoked when the merged exclusion policy changes.
func (w *ConfigMapWatcher) OnPolicyChange(fn func()) {
	w.onChange = fn
}

// LoadInitial synchronously loads the exclusion ConfigMap before workload reconciliation.
func (w *ConfigMapWatcher) LoadInitial(ctx context.Context) error {
	if w.namespace == "" || w.name == "" {
		w.logger.Info("configmap watcher disabled")
		return nil
	}

	cm, err := w.clientset.CoreV1().ConfigMaps(w.namespace).Get(ctx, w.name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			w.logger.Warn("exclusion configmap not found; using base policy only",
				"namespace", w.namespace,
				"name", w.name,
			)
			return nil
		}
		return fmt.Errorf("load exclusion configmap: %w", err)
	}

	w.applyConfigMap(cm, false)
	return nil
}

// Watch hot-reloads exclusion policy when the ConfigMap changes.
func (w *ConfigMapWatcher) Watch(ctx context.Context) error {
	if w.namespace == "" || w.name == "" {
		return nil
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		w.clientset,
		0,
		informers.WithNamespace(w.namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fmt.Sprintf("metadata.name=%s", w.name)
		}),
	)

	informer := factory.Core().V1().ConfigMaps().Informer()
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handleConfigMap(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			w.handleConfigMap(newObj)
		},
		DeleteFunc: func(_ interface{}) {
			w.logger.Warn("exclusion configmap deleted; reverting to base policy",
				"namespace", w.namespace,
				"name", w.name,
			)
			w.storePolicy(w.basePolicy, true)
		},
	}); err != nil {
		return fmt.Errorf("register configmap handler: %w", err)
	}

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("sync exclusion configmap informer")
	}

	w.logger.Info("watching exclusion configmap", "namespace", w.namespace, "name", w.name)
	<-ctx.Done()
	return nil
}

func (w *ConfigMapWatcher) handleConfigMap(obj interface{}) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return
	}
	w.applyConfigMap(cm, true)
}

func (w *ConfigMapWatcher) applyConfigMap(cm *corev1.ConfigMap, notify bool) {
	filePolicy, err := ParseConfigMapData(cm.Data)
	if err != nil {
		w.logger.Error("invalid exclusion configmap; keeping current policy",
			"namespace", w.namespace,
			"name", w.name,
			"error", err,
		)
		return
	}
	merged := Merge(w.basePolicy, filePolicy)
	w.storePolicy(merged, notify)
	w.logger.Info("loaded exclusion policy",
		"namespaces", len(merged.Namespaces),
		"workloads", len(merged.Workloads),
	)
}

func (w *ConfigMapWatcher) storePolicy(policy Policy, notify bool) {
	prev := w.Current()
	if prev.Equal(policy) {
		return
	}
	w.policy.Store(policy)
	if notify && w.onChange != nil {
		w.onChange()
	}
}
