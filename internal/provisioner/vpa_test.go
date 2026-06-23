// Copyright 2026 RunWhen
// SPDX-License-Identifier: Apache-2.0

package provisioner

import (
	"context"
	"strings"
	"testing"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
)

func TestBuildVPA(t *testing.T) {
	ref := workloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "default",
		Name:       "nginx",
		UID:        "abc-123",
	}

	vpa := buildVPA(ref)
	if vpa.GetName() != "nginx-deployment-vpa" {
		t.Fatalf("name = %q, want nginx-deployment-vpa", vpa.GetName())
	}
	if vpa.GetNamespace() != "default" {
		t.Fatalf("namespace = %q, want default", vpa.GetNamespace())
	}

	updateMode, found, err := unstructured.NestedString(vpa.Object, "spec", "updatePolicy", "updateMode")
	if err != nil || !found || updateMode != "Off" {
		t.Fatalf("updateMode = %q found=%v err=%v, want Off", updateMode, found, err)
	}

	targetKind, found, err := unstructured.NestedString(vpa.Object, "spec", "targetRef", "kind")
	if err != nil || !found || targetKind != "Deployment" {
		t.Fatalf("targetRef.kind = %q found=%v err=%v", targetKind, found, err)
	}

	owners := vpa.GetOwnerReferences()
	if len(owners) != 1 {
		t.Fatalf("ownerReferences len = %d, want 1", len(owners))
	}
	if owners[0].UID != types.UID(ref.UID) {
		t.Fatalf("owner UID = %q, want %q", owners[0].UID, ref.UID)
	}
	if owners[0].Controller == nil || !*owners[0].Controller {
		t.Fatal("expected controller owner reference")
	}

	labels := vpa.GetLabels()
	if labels[ManagedByLabelKey] != ManagedByLabelValue {
		t.Fatalf("managed label = %q, want %q", labels[ManagedByLabelKey], ManagedByLabelValue)
	}
}

func TestVPANameForIncludesKind(t *testing.T) {
	deployName := vpaNameFor("Deployment", "api")
	statefulName := vpaNameFor("StatefulSet", "api")
	if deployName == statefulName {
		t.Fatalf("expected distinct VPA names, both got %q", deployName)
	}
	if deployName != "api-deployment-vpa" {
		t.Fatalf("deploy VPA name = %q, want api-deployment-vpa", deployName)
	}
	if statefulName != "api-statefulset-vpa" {
		t.Fatalf("statefulset VPA name = %q, want api-statefulset-vpa", statefulName)
	}
}

func TestVPANameForTruncatesLongNames(t *testing.T) {
	longName := strings.Repeat("a", 80)
	got := vpaNameFor("StatefulSet", longName)
	if len(got) > maxKubernetesNameLength {
		t.Fatalf("name length = %d, want <= %d (%q)", len(got), maxKubernetesNameLength, got)
	}
	if !strings.HasSuffix(got, "-statefulset-vpa") {
		t.Fatalf("expected statefulset suffix, got %q", got)
	}
}

func TestEnsureVPAExistsCreatesWhenMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	ref := workloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "default",
		Name:       "web",
		UID:        "uid-1",
	}

	if err := ensureVPAExists(context.Background(), client, ref); err != nil {
		t.Fatalf("ensureVPAExists() error = %v", err)
	}

	got, err := client.Resource(vpaGVR).Namespace("default").Get(
		context.Background(),
		"web-deployment-vpa",
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	kind, _, _ := unstructured.NestedString(got.Object, "spec", "targetRef", "kind")
	if kind != "Deployment" {
		t.Fatalf("targetRef.kind = %q, want Deployment", kind)
	}
}

func TestEnsureVPAExistsIdempotent(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	ref := workloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "default",
		Name:       "web",
		UID:        "uid-1",
	}
	for range 2 {
		if err := ensureVPAExists(context.Background(), client, ref); err != nil {
			t.Fatalf("ensureVPAExists() error = %v", err)
		}
	}
}

func TestWorkloadRefFromMeta(t *testing.T) {
	meta := metav1.ObjectMeta{
		Namespace:   "apps",
		Name:        "web",
		UID:         "uid-1",
		Labels:      map[string]string{"app": "web"},
		Annotations: map[string]string{exclusion.SkipOptOutKey: exclusion.SkipOptOutValue},
	}
	ref := workloadRefFromMeta("apps/v1", "StatefulSet", meta)
	if ref.Kind != "StatefulSet" || ref.Name != "web" || ref.Namespace != "apps" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	if !ref.exclusionRef().HasOptOut() {
		t.Fatal("expected annotation opt-out")
	}
}

func TestEnsureVPASkipsOptOut(t *testing.T) {
	ref := workloadRef{
		Kind:        "Deployment",
		Namespace:   "default",
		Name:        "skip-me",
		Annotations: map[string]string{exclusion.SkipOptOutKey: exclusion.SkipOptOutValue},
	}
	policy := exclusion.Empty()
	if !policy.ShouldSkip(ref.exclusionRef()) {
		t.Fatal("expected workload to be skipped")
	}
}

func TestReconcileVPADeletesWhenExcluded(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	ref := workloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "default",
		Name:       "web",
		UID:        "uid-1",
	}

	if err := ensureVPAExists(context.Background(), client, ref); err != nil {
		t.Fatalf("ensureVPAExists() error = %v", err)
	}

	skipPolicy := exclusion.Empty()
	skipPolicy.Namespaces = map[string]struct{}{"default": {}}
	if err := reconcileVPA(context.Background(), client, ref, skipPolicy); err != nil {
		t.Fatalf("reconcileVPA() error = %v", err)
	}

	_, err := client.Resource(vpaGVR).Namespace("default").Get(
		context.Background(),
		"web-deployment-vpa",
		metav1.GetOptions{},
	)
	if err == nil {
		t.Fatal("expected managed VPA to be deleted when workload excluded")
	}
}

func TestDeleteManagedVPASkipsForeignVPA(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	ref := workloadRef{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Namespace:  "default",
		Name:       "web",
		UID:        "uid-1",
	}

	foreign := buildVPA(ref)
	foreign.SetLabels(nil)
	_ = unstructured.SetNestedField(foreign.Object, map[string]interface{}{
		"updateMode": "Auto",
	}, "spec", "updatePolicy")
	if _, err := client.Resource(vpaGVR).Namespace("default").Create(
		context.Background(),
		foreign,
		metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := deleteManagedVPA(context.Background(), client, ref); err != nil {
		t.Fatalf("deleteManagedVPA() error = %v", err)
	}

	if _, err := client.Resource(vpaGVR).Namespace("default").Get(
		context.Background(),
		"web-deployment-vpa",
		metav1.GetOptions{},
	); err != nil {
		t.Fatalf("foreign VPA should remain: %v", err)
	}
}
