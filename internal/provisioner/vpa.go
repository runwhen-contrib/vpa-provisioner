package provisioner

import (
	"context"
	"fmt"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
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

func vpaNameFor(workloadName string) string {
	return fmt.Sprintf("%s-vpa", workloadName)
}

func buildVPA(ref workloadRef) *unstructured.Unstructured {
	vpa := &unstructured.Unstructured{}
	vpa.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   vpaGVR.Group,
		Version: vpaGVR.Version,
		Kind:    "VerticalPodAutoscaler",
	})
	vpa.SetName(vpaNameFor(ref.Name))
	vpa.SetNamespace(ref.Namespace)
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

func ensureVPAExists(ctx context.Context, client dynamic.Interface, ref workloadRef, policy exclusion.Policy) error {
	if policy.ShouldSkip(ref.exclusionRef()) {
		return nil
	}

	name := vpaNameFor(ref.Name)
	nsClient := client.Resource(vpaGVR).Namespace(ref.Namespace)

	_, err := nsClient.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	vpa := buildVPA(ref)
	_, err = nsClient.Create(ctx, vpa, metav1.CreateOptions{})
	return err
}
