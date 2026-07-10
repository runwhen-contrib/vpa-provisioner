// Copyright 2026 RunWhen
// SPDX-License-Identifier: Apache-2.0

package prerequisites

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"
)

func newFakeDiscovery(resources []*metav1.APIResourceList) *discoveryfake.FakeDiscovery {
	dc := &discoveryfake.FakeDiscovery{Fake: &clientgotesting.Fake{}}
	dc.Resources = resources
	return dc
}

func TestVpaAPIAvailable(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		dc := newFakeDiscovery([]*metav1.APIResourceList{
			{
				GroupVersion: "autoscaling.k8s.io/v1",
				APIResources: []metav1.APIResource{
					{Kind: "VerticalPodAutoscaler", Name: "verticalpodautoscalers"},
				},
			},
		})
		ok, err := vpaAPIAvailable(dc)
		if err != nil || !ok {
			t.Fatalf("vpaAPIAvailable() = (%v, %v), want (true, nil)", ok, err)
		}
	})

	t.Run("missing group", func(t *testing.T) {
		dc := newFakeDiscovery(nil)
		ok, err := vpaAPIAvailable(dc)
		if err != nil || ok {
			t.Fatalf("vpaAPIAvailable() = (%v, %v), want (false, nil)", ok, err)
		}
	})
}

func TestCheckerVerify(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	clientset := k8sfake.NewSimpleClientset()
	discoveryClient := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "autoscaling.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Kind: "VerticalPodAutoscaler", Name: "verticalpodautoscalers"},
			},
		},
	})

	checker := NewChecker(clientset, dynamicClient, discoveryClient, Config{})
	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestCheckerVerifyMissingCRD(t *testing.T) {
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClient(scheme)
	clientset := k8sfake.NewSimpleClientset()
	discoveryClient := newFakeDiscovery(nil)

	checker := NewChecker(clientset, dynamicClient, discoveryClient, Config{})
	err := checker.Verify(context.Background())
	if err == nil {
		t.Fatal("expected error for missing CRD")
	}
	if got := err.Error(); !strings.Contains(got, "VPA CRD not installed") || !strings.Contains(got, vpaCRDName) {
		t.Fatalf("error = %q", got)
	}
}

func TestCheckerRequireRecommender(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	clientset := k8sfake.NewSimpleClientset()
	discoveryClient := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "autoscaling.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Kind: "VerticalPodAutoscaler", Name: "verticalpodautoscalers"},
			},
		},
	})

	checker := NewChecker(clientset, dynamicClient, discoveryClient, Config{RequireRecommender: true})
	if err := checker.Verify(context.Background()); err == nil {
		t.Fatal("expected error when recommender required but missing")
	}
}

func TestCheckerVerifyWithRecommender(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	clientset := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      recommenderDeployName,
				Namespace: metav1.NamespaceSystem,
			},
		},
	)
	discoveryClient := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "autoscaling.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Kind: "VerticalPodAutoscaler", Name: "verticalpodautoscalers"},
			},
		},
	})

	checker := NewChecker(clientset, dynamicClient, discoveryClient, Config{RequireRecommender: true})
	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestCheckerVerifyWithPrefixedRecommender(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		vpaGVR: "VerticalPodAutoscalerList",
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	clientset := k8sfake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vault-vpa-recommender",
				Namespace: "vpa",
			},
		},
	)
	discoveryClient := newFakeDiscovery([]*metav1.APIResourceList{
		{
			GroupVersion: "autoscaling.k8s.io/v1",
			APIResources: []metav1.APIResource{
				{Kind: "VerticalPodAutoscaler", Name: "verticalpodautoscalers"},
			},
		},
	})

	checker := NewChecker(clientset, dynamicClient, discoveryClient, Config{RequireRecommender: true})
	if err := checker.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestMatchesComponentName(t *testing.T) {
	cases := []struct {
		deployment string
		component  string
		want       bool
	}{
		{"vpa-recommender", recommenderDeployName, true},
		{"vault-vpa-recommender", recommenderDeployName, true},
		{"vpa-admission-controller", admissionDeployName, true},
		{"vault-vpa-admission-controller", admissionDeployName, true},
		{"nginx", recommenderDeployName, false},
	}
	for _, tc := range cases {
		if got := MatchesComponentName(tc.deployment, tc.component); got != tc.want {
			t.Fatalf("MatchesComponentName(%q, %q) = %v, want %v", tc.deployment, tc.component, got, tc.want)
		}
	}
}

func TestHasComponentDeployment(t *testing.T) {
	deployments := []appsv1.Deployment{
		{ObjectMeta: metav1.ObjectMeta{Name: "vpa-recommender"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "other"}},
	}
	if !HasComponentDeployment(deployments, recommenderDeployName) {
		t.Fatal("expected recommender deployment to be found")
	}
	if HasComponentDeployment(deployments, updaterDeployName) {
		t.Fatal("did not expect updater deployment")
	}
}

func TestErrorHelpers(t *testing.T) {
	if !isForbidden(errors.New("verticalpodautoscalers.autoscaling.k8s.io is forbidden")) {
		t.Fatal("expected forbidden detection")
	}
	if !isAPINotFound(errors.New("the server could not find the requested resource")) {
		t.Fatal("expected API not found detection")
	}
}
