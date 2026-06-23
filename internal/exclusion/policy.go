package exclusion

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SkipOptOutKey opts a workload out of VPA provisioning when set to SkipOptOutValue
	// on labels or annotations.
	SkipOptOutKey   = "vpa-provisioner/skip"
	SkipOptOutValue = "true"

	configDataKey = "config.yaml"
)

// WorkloadRef identifies a namespaced Deployment or StatefulSet.
type WorkloadRef struct {
	Kind       string
	Namespace  string
	Name       string
	Labels     map[string]string
	Annotations map[string]string
}

// WorkloadKey uniquely identifies a workload within the cluster.
type WorkloadKey struct {
	Namespace string
	Kind      string
	Name      string
}

func (k WorkloadKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.Namespace, k.Kind, k.Name)
}

// Policy holds explicit namespace and workload exclusions.
type Policy struct {
	Namespaces map[string]struct{}
	Workloads  map[WorkloadKey]struct{}
}

// FileConfig is the YAML schema stored in the exclusion ConfigMap.
type FileConfig struct {
	ExcludeNamespaces []string              `yaml:"excludeNamespaces"`
	ExcludeWorkloads  []FileWorkloadExclude `yaml:"excludeWorkloads"`
}

// FileWorkloadExclude identifies a workload to omit from VPA provisioning.
type FileWorkloadExclude struct {
	Namespace string `yaml:"namespace"`
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
}

// Empty returns a policy with initialized maps.
func Empty() Policy {
	return Policy{
		Namespaces: map[string]struct{}{},
		Workloads:  map[WorkloadKey]struct{}{},
	}
}

// ParseNamespaces parses a comma-separated namespace list.
func ParseNamespaces(raw string) Policy {
	policy := Empty()
	for _, ns := range strings.Split(raw, ",") {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		policy.Namespaces[ns] = struct{}{}
	}
	return policy
}

// ParseConfigMapData parses exclusion rules from a ConfigMap data entry.
func ParseConfigMapData(data map[string]string) (Policy, error) {
	raw, ok := data[configDataKey]
	if !ok || strings.TrimSpace(raw) == "" {
		return Empty(), nil
	}
	return ParseYAML([]byte(raw))
}

// ParseYAML parses exclusion rules from YAML bytes.
func ParseYAML(raw []byte) (Policy, error) {
	var cfg FileConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Empty(), fmt.Errorf("parse exclusion config: %w", err)
	}
	return FromFileConfig(cfg)
}

// FromFileConfig converts a file config into a Policy.
func FromFileConfig(cfg FileConfig) (Policy, error) {
	policy := Empty()

	for _, ns := range cfg.ExcludeNamespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		policy.Namespaces[ns] = struct{}{}
	}

	for _, workload := range cfg.ExcludeWorkloads {
		key, err := workload.Key()
		if err != nil {
			return Empty(), err
		}
		policy.Workloads[key] = struct{}{}
	}

	return policy, nil
}

func (w FileWorkloadExclude) Key() (WorkloadKey, error) {
	namespace := strings.TrimSpace(w.Namespace)
	kind := strings.TrimSpace(w.Kind)
	name := strings.TrimSpace(w.Name)
	if namespace == "" || kind == "" || name == "" {
		return WorkloadKey{}, fmt.Errorf("excludeWorkloads entry must set namespace, kind, and name")
	}
	switch kind {
	case "Deployment", "StatefulSet":
	default:
		return WorkloadKey{}, fmt.Errorf("excludeWorkloads kind %q must be Deployment or StatefulSet", kind)
	}
	return WorkloadKey{Namespace: namespace, Kind: kind, Name: name}, nil
}

// Merge combines multiple policies. Later policies do not remove earlier entries.
func Merge(policies ...Policy) Policy {
	merged := Empty()
	for _, policy := range policies {
		for ns := range policy.Namespaces {
			merged.Namespaces[ns] = struct{}{}
		}
		for key := range policy.Workloads {
			merged.Workloads[key] = struct{}{}
		}
	}
	return merged
}

// Equal reports whether two policies contain the same exclusions.
func (p Policy) Equal(other Policy) bool {
	if len(p.Namespaces) != len(other.Namespaces) {
		return false
	}
	for ns := range p.Namespaces {
		if _, ok := other.Namespaces[ns]; !ok {
			return false
		}
	}
	if len(p.Workloads) != len(other.Workloads) {
		return false
	}
	for key := range p.Workloads {
		if _, ok := other.Workloads[key]; !ok {
			return false
		}
	}
	return true
}

// ShouldSkip reports whether a workload should not receive a VPA.
func (p Policy) ShouldSkip(ref WorkloadRef) bool {
	if ref.HasOptOut() {
		return true
	}
	if _, excluded := p.Namespaces[ref.Namespace]; excluded {
		return true
	}
	key := WorkloadKey{Namespace: ref.Namespace, Kind: ref.Kind, Name: ref.Name}
	_, excluded := p.Workloads[key]
	return excluded
}

// HasOptOut reports whether the workload opted out via label or annotation.
func (ref WorkloadRef) HasOptOut() bool {
	if ref.Labels != nil && ref.Labels[SkipOptOutKey] == SkipOptOutValue {
		return true
	}
	if ref.Annotations != nil && ref.Annotations[SkipOptOutKey] == SkipOptOutValue {
		return true
	}
	return false
}

// ConfigMapDataKey returns the ConfigMap data key holding exclusion YAML.
func ConfigMapDataKey() string {
	return configDataKey
}
