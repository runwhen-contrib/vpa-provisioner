package provisioner

import (
	"context"
	"fmt"
	"strings"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

const maxKubernetesNameLength = 63

const (
	// ManagedByLabelKey marks VPAs created and owned by this controller.
	ManagedByLabelKey   = "vpa-provisioner.runwhen.com/managed"
	ManagedByLabelValue = "true"
)

var vpaGVR = schema.GroupVersionResource{
	Group:    "autoscaling.k8s.io",
	Version:  "v1",
	Resource: "verticalpodautoscalers",
}

type workloadRef struct {
	APIVersion  string
	Kind        string
	Namespace   string
	Name        string
	UID         string
	Labels      map[string]string
	Annotations map[string]string
}

func (w workloadRef) exclusionRef() exclusion.WorkloadRef {
	return exclusion.WorkloadRef{
		Kind:        w.Kind,
		Namespace:   w.Namespace,
		Name:        w.Name,
		Labels:      w.Labels,
		Annotations: w.Annotations,
	}
}

func vpaNameFor(kind, workloadName string) string {
	suffix := "-" + strings.ToLower(kind) + "-vpa"
	maxBaseLen := maxKubernetesNameLength - len(suffix)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	if len(workloadName) > maxBaseLen {
		workloadName = strings.Trim(workloadName[:maxBaseLen], "-")
	}
	return workloadName + suffix
}

func buildVPA(ref workloadRef) *unstructured.Unstructured {
	vpa := &unstructured.Unstructured{}
	vpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vpaGVR.Group,
		Version: vpaGVR.Version,
		Kind:    "VerticalPodAutoscaler",
	})
	vpa.SetName(vpaNameFor(ref.Kind, ref.Name))
	vpa.SetNamespace(ref.Namespace)
	vpa.SetLabels(map[string]string{
		ManagedByLabelKey: ManagedByLabelValue,
	})
	vpa.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: ref.APIVersion,
			Kind:       ref.Kind,
			Name:       ref.Name,
			UID:        types.UID(ref.UID),
			Controller: boolPtr(true),
		},
	})
	_ = unstructured.SetNestedField(vpa.Object, map[string]interface{}{
		"apiVersion": ref.APIVersion,
		"kind":       ref.Kind,
		"name":       ref.Name,
	}, "spec", "targetRef")
	_ = unstructured.SetNestedField(vpa.Object, map[string]interface{}{
		"updateMode": "Off",
	}, "spec", "updatePolicy")
	return vpa
}

func boolPtr(v bool) *bool {
	return &v
}

// reconcileVPA ensures a managed VPA exists for eligible workloads and removes
// managed VPAs when the workload is excluded or no longer provisioned.
func reconcileVPA(ctx context.Context, client dynamic.Interface, ref workloadRef, policy exclusion.Policy) error {
	if policy.ShouldSkip(ref.exclusionRef()) {
		return deleteManagedVPA(ctx, client, ref)
	}
	return ensureVPAExists(ctx, client, ref)
}

func deleteManagedVPA(ctx context.Context, client dynamic.Interface, ref workloadRef) error {
	name := vpaNameFor(ref.Kind, ref.Name)
	nsClient := client.Resource(vpaGVR).Namespace(ref.Namespace)

	vpa, err := nsClient.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get VPA %s: %w", name, err)
	}
	if !isProvisionerManagedVPA(vpa, ref) {
		return nil
	}

	if err := nsClient.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete VPA %s: %w", name, err)
	}
	return nil
}

// isProvisionerManagedVPA reports whether a VPA was created by this controller
// for the given workload. Foreign VPAs (manual installs, other tools) are never matched.
func isProvisionerManagedVPA(vpa *unstructured.Unstructured, ref workloadRef) bool {
	if vpa.GetName() != vpaNameFor(ref.Kind, ref.Name) {
		return false
	}

	updateMode, found, err := unstructured.NestedString(vpa.Object, "spec", "updatePolicy", "updateMode")
	if err != nil || !found || updateMode != "Off" {
		return false
	}

	targetKind, found, err := unstructured.NestedString(vpa.Object, "spec", "targetRef", "kind")
	if err != nil || !found || targetKind != ref.Kind {
		return false
	}
	targetName, found, err := unstructured.NestedString(vpa.Object, "spec", "targetRef", "name")
	if err != nil || !found || targetName != ref.Name {
		return false
	}

	if labels := vpa.GetLabels(); labels != nil && labels[ManagedByLabelKey] == ManagedByLabelValue {
		return true
	}

	// VPAs created before the managed label used ownerReferences only.
	for _, owner := range vpa.GetOwnerReferences() {
		if owner.Controller == nil || !*owner.Controller {
			continue
		}
		if owner.Kind == ref.Kind && owner.Name == ref.Name && string(owner.UID) == ref.UID {
			return true
		}
	}
	return false
}

func ensureVPAExists(ctx context.Context, client dynamic.Interface, ref workloadRef) error {
	name := vpaNameFor(ref.Kind, ref.Name)
	nsClient := client.Resource(vpaGVR).Namespace(ref.Namespace)

	existing, err := nsClient.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if !isProvisionerManagedVPA(existing, ref) {
			return nil
		}
		staleOwner := false
		for _, owner := range existing.GetOwnerReferences() {
			if owner.Controller != nil && *owner.Controller && string(owner.UID) != ref.UID {
				staleOwner = true
				break
			}
		}
		if !staleOwner {
			return nil
		}
		if err := nsClient.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete stale VPA %s: %w", name, err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get VPA %s: %w", name, err)
	}

	vpa := buildVPA(ref)
	_, err = nsClient.Create(ctx, vpa, metav1.CreateOptions{})
	if err == nil || apierrors.IsAlreadyExists(err) {
		return nil
	}
	return fmt.Errorf("create VPA %s: %w", name, err)
}
