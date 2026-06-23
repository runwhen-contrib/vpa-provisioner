package provisioner

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func workloadRefFromMeta(apiVersion, kind string, meta metav1.ObjectMeta) workloadRef {
	return workloadRef{
		APIVersion:  apiVersion,
		Kind:        kind,
		Namespace:   meta.Namespace,
		Name:        meta.Name,
		UID:         string(meta.UID),
		Labels:      meta.Labels,
		Annotations: meta.Annotations,
	}
}

func workloadRefFromObject(obj interface{}) (workloadRef, bool) {
	switch workload := obj.(type) {
	case *appsv1.Deployment:
		return workloadRefFromMeta("apps/v1", "Deployment", workload.ObjectMeta), true
	case *appsv1.StatefulSet:
		return workloadRefFromMeta("apps/v1", "StatefulSet", workload.ObjectMeta), true
	default:
		return workloadRef{}, false
	}
}
