// Copyright 2026 RunWhen
// SPDX-License-Identifier: Apache-2.0

package exclusion

import (
	"strings"
	"testing"
)

func TestParseNamespaces(t *testing.T) {
	policy := ParseNamespaces(" kube-system,monitoring,,cert-manager ")
	if len(policy.Namespaces) != 3 {
		t.Fatalf("namespaces len = %d, want 3", len(policy.Namespaces))
	}
	for _, ns := range []string{"kube-system", "monitoring", "cert-manager"} {
		if _, ok := policy.Namespaces[ns]; !ok {
			t.Fatalf("missing namespace %q", ns)
		}
	}
}

func TestParseYAML(t *testing.T) {
	raw := []byte(`
excludeNamespaces:
  - monitoring
excludeWorkloads:
  - namespace: default
    kind: Deployment
    name: nginx
  - namespace: data
    kind: StatefulSet
    name: redis
`)
	policy, err := ParseYAML(raw)
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	if _, ok := policy.Namespaces["monitoring"]; !ok {
		t.Fatal("expected monitoring namespace")
	}
	key := WorkloadKey{Namespace: "default", Kind: "Deployment", Name: "nginx"}
	if _, ok := policy.Workloads[key]; !ok {
		t.Fatal("expected nginx workload exclusion")
	}
}

func TestShouldSkip(t *testing.T) {
	policy := Merge(
		ParseNamespaces("kube-system"),
		mustPolicy(t, `
excludeWorkloads:
  - namespace: apps
    kind: Deployment
    name: api
`),
	)

	tests := []struct {
		name string
		ref  WorkloadRef
		want bool
	}{
		{
			name: "excluded namespace",
			ref:  WorkloadRef{Namespace: "kube-system", Kind: "Deployment", Name: "coredns"},
			want: true,
		},
		{
			name: "excluded workload",
			ref:  WorkloadRef{Namespace: "apps", Kind: "Deployment", Name: "api"},
			want: true,
		},
		{
			name: "opt-out label",
			ref: WorkloadRef{
				Namespace: "default",
				Kind:      "Deployment",
				Name:      "web",
				Labels:    map[string]string{SkipOptOutKey: SkipOptOutValue},
			},
			want: true,
		},
		{
			name: "opt-out annotation",
			ref: WorkloadRef{
				Namespace:   "default",
				Kind:        "Deployment",
				Name:        "web",
				Annotations: map[string]string{SkipOptOutKey: SkipOptOutValue},
			},
			want: true,
		},
		{
			name: "allowed workload",
			ref:  WorkloadRef{Namespace: "default", Kind: "Deployment", Name: "web"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := policy.ShouldSkip(tt.ref); got != tt.want {
				t.Fatalf("ShouldSkip() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseYAMLInvalidWorkload(t *testing.T) {
	_, err := ParseYAML([]byte(`
excludeWorkloads:
  - namespace: default
    kind: CronJob
    name: job
`))
	if err == nil || !strings.Contains(err.Error(), "Deployment or StatefulSet") {
		t.Fatalf("expected kind validation error, got %v", err)
	}
}

func mustPolicy(t *testing.T, raw string) Policy {
	t.Helper()
	policy, err := ParseYAML([]byte(raw))
	if err != nil {
		t.Fatalf("ParseYAML() error = %v", err)
	}
	return policy
}
