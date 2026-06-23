package exclusion

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestConfigMapWatcherLoadInitialMissing(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset()
	watcher := NewConfigMapWatcher(clientset, "vpa-provisioner", "vpa-provisioner-config", ParseNamespaces("kube-system"))

	if err := watcher.LoadInitial(context.Background()); err != nil {
		t.Fatalf("LoadInitial() error = %v", err)
	}
	if len(watcher.Current().Namespaces) != 1 {
		t.Fatalf("namespaces = %d, want 1 base namespace", len(watcher.Current().Namespaces))
	}
}

func TestConfigMapWatcherLoadInitialInvalidYAML(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpa-provisioner-config",
			Namespace: "vpa-provisioner",
		},
		Data: map[string]string{
			"config.yaml": "excludeNamespaces: [",
		},
	})
	base := ParseNamespaces("kube-system")
	watcher := NewConfigMapWatcher(clientset, "vpa-provisioner", "vpa-provisioner-config", base)

	if err := watcher.LoadInitial(context.Background()); err != nil {
		t.Fatalf("LoadInitial() error = %v, want nil on invalid YAML", err)
	}
	if len(watcher.Current().Namespaces) != 1 {
		t.Fatalf("expected base policy after invalid YAML, got %d namespaces", len(watcher.Current().Namespaces))
	}
}

func TestConfigMapWatcherLoadInitialMergesPolicy(t *testing.T) {
	clientset := k8sfake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "vpa-provisioner-config",
			Namespace: "vpa-provisioner",
		},
		Data: map[string]string{
			"config.yaml": `
excludeNamespaces:
  - monitoring
`,
		},
	})
	watcher := NewConfigMapWatcher(clientset, "vpa-provisioner", "vpa-provisioner-config", ParseNamespaces("kube-system"))

	if err := watcher.LoadInitial(context.Background()); err != nil {
		t.Fatalf("LoadInitial() error = %v", err)
	}
	current := watcher.Current()
	if _, ok := current.Namespaces["kube-system"]; !ok {
		t.Fatal("expected base namespace kube-system")
	}
	if _, ok := current.Namespaces["monitoring"]; !ok {
		t.Fatal("expected monitoring namespace from configmap")
	}
}

func TestConfigMapWatcherApplyInvalidYAMLKeepsCurrent(t *testing.T) {
	watcher := NewConfigMapWatcher(k8sfake.NewSimpleClientset(), "vpa-provisioner", "vpa-provisioner-config", ParseNamespaces("kube-system"))
	watcher.applyConfigMap(&corev1.ConfigMap{
		Data: map[string]string{
			"config.yaml": `
excludeNamespaces:
  - monitoring
`,
		},
	})
	watcher.applyConfigMap(&corev1.ConfigMap{
		Data: map[string]string{
			"config.yaml": "excludeNamespaces: [",
		},
	})

	current := watcher.Current()
	if _, ok := current.Namespaces["monitoring"]; !ok {
		t.Fatal("expected previous policy to remain after invalid update")
	}
}
