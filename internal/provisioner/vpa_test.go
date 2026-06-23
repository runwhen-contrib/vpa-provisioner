package provisioner

import (
	"testing"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
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
	if vpa.GetName() != "nginx-vpa" {
		t.Fatalf("name = %q, want nginx-vpa", vpa.GetName())
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
}

func TestVPANameFor(t *testing.T) {
	if got := vpaNameFor("api"); got != "api-vpa" {
		t.Fatalf("vpaNameFor() = %q, want api-vpa", got)
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
