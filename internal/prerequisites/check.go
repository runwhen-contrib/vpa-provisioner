package prerequisites

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	vpaCRDName = "verticalpodautoscalers.autoscaling.k8s.io"

	recommenderDeployName = "vpa-recommender"
	admissionDeployName   = "vpa-admission-controller"
	updaterDeployName     = "vpa-updater"
)

var vpaGVR = schema.GroupVersionResource{
	Group:    "autoscaling.k8s.io",
	Version:  "v1",
	Resource: "verticalpodautoscalers",
}

// Config controls startup prerequisite validation.
type Config struct {
	// RequireRecommender fails startup when the VPA Recommender is not detected.
	RequireRecommender bool
}

// Checker validates cluster prerequisites before the controller starts.
type Checker struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	discovery     discovery.DiscoveryInterface
	logger        *slog.Logger
	cfg           Config
}

func NewChecker(
	clientset kubernetes.Interface,
	dynamicClient dynamic.Interface,
	discoveryClient discovery.DiscoveryInterface,
	cfg Config,
) *Checker {
	return &Checker{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		discovery:     discoveryClient,
		logger:        slog.Default().With("component", "prerequisites"),
		cfg:           cfg,
	}
}

// Verify checks required cluster prerequisites and logs advisory warnings.
func (c *Checker) Verify(ctx context.Context) error {
	if err := c.checkVPAAPI(ctx); err != nil {
		return err
	}
	if err := c.checkVPAPermissions(ctx); err != nil {
		return err
	}

	recommenderFound := c.checkRecommender(ctx)
	admissionFound, updaterFound := c.checkMutatingComponents(ctx)
	c.checkVPAWebhooks(ctx)

	if admissionFound || updaterFound {
		c.logger.Warn(
			"VPA Admission Controller and/or Updater detected; vpa-provisioner expects updateMode Off with recommender only",
			"admission_controller", admissionFound,
			"updater", updaterFound,
		)
	}

	if !recommenderFound {
		msg := "VPA Recommender deployment not found; VPA objects will be created but recommendations will not be generated until the recommender is running"
		if c.cfg.RequireRecommender {
			return errors.New(msg)
		}
		c.logger.Warn(msg)
	}

	c.logger.Info("cluster prerequisites verified")
	return nil
}

func (c *Checker) checkVPAAPI(ctx context.Context) error {
	available, err := vpaAPIAvailable(c.discovery)
	if err != nil {
		return fmt.Errorf("discover VPA API: %w", err)
	}
	if !available {
		return fmt.Errorf(
			"VPA CRD not installed (missing %s); install the Vertical Pod Autoscaler CRD before running vpa-provisioner",
			vpaCRDName,
		)
	}

	c.logger.Info("VPA API available", "crd", vpaCRDName)
	return nil
}

func vpaAPIAvailable(dc discovery.DiscoveryInterface) (bool, error) {
	resourceList, err := dc.ServerResourcesForGroupVersion("autoscaling.k8s.io/v1")
	if err != nil {
		if discovery.IsGroupDiscoveryFailedError(err) {
			return false, nil
		}
		if metaIsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	for _, resource := range resourceList.APIResources {
		if resource.Kind == "VerticalPodAutoscaler" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Checker) checkVPAPermissions(ctx context.Context) error {
	_, err := c.dynamicClient.Resource(vpaGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil {
		c.logger.Info("VPA RBAC verified")
		return nil
	}
	if isForbidden(err) {
		return fmt.Errorf(
			"insufficient RBAC to list verticalpodautoscalers; grant get/list/watch/create on autoscaling.k8s.io/verticalpodautoscalers: %w",
			err,
		)
	}
	if isAPINotFound(err) {
		return fmt.Errorf("VPA API unavailable while listing verticalpodautoscalers: %w", err)
	}
	return fmt.Errorf("verify VPA permissions: %w", err)
}

func (c *Checker) checkRecommender(ctx context.Context) bool {
	if c.deploymentExists(ctx, recommenderDeployName) {
		c.logger.Info("VPA Recommender detected", "deployment", recommenderDeployName)
		return true
	}
	return false
}

func (c *Checker) checkMutatingComponents(ctx context.Context) (admissionFound, updaterFound bool) {
	admissionFound = c.deploymentExists(ctx, admissionDeployName)
	updaterFound = c.deploymentExists(ctx, updaterDeployName)
	return admissionFound, updaterFound
}

func (c *Checker) checkVPAWebhooks(ctx context.Context) {
	webhooks, err := c.clientset.AdmissionregistrationV1().MutatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
	if err != nil {
		c.logger.Debug("unable to list mutating webhooks", "error", err)
		return
	}

	for _, webhook := range webhooks.Items {
		if !strings.Contains(strings.ToLower(webhook.Name), "vpa") {
			continue
		}
		c.logger.Warn(
			"VPA mutating webhook configuration detected; ensure VPA Admission Controller is not mutating pods if using updateMode Off only",
			"webhook", webhook.Name,
		)
	}
}

func (c *Checker) deploymentExists(ctx context.Context, name string) bool {
	namespaces := []string{
		metav1.NamespaceSystem,
		"vpa-system",
		"vpa",
		"vertical-pod-autoscaler",
		"kube-vpa",
	}

	for _, namespace := range namespaces {
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil && deployment != nil {
			c.logger.Debug("found VPA component deployment", "deployment", name, "namespace", namespace)
			return true
		}
	}

	return false
}

func isForbidden(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

func isAPINotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "could not find the requested resource") ||
		strings.Contains(msg, "no matches for kind")
}

func metaIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// DeploymentNames returns known VPA component deployment names (for tests/docs).
func DeploymentNames() (recommender, admission, updater string) {
	return recommenderDeployName, admissionDeployName, updaterDeployName
}

// HasComponentDeployment reports whether a deployment list contains a named VPA component.
func HasComponentDeployment(deployments []appsv1.Deployment, name string) bool {
	for i := range deployments {
		if deployments[i].Name == name {
			return true
		}
	}
	return false
}
