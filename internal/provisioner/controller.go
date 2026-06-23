package provisioner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runwhen-contrib/vpa-provisioner/internal/exclusion"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const defaultResyncPeriod = 30 * time.Minute

// Config holds runtime options for the provisioner controller.
type Config struct {
	BaseExclusion      exclusion.Policy
	ConfigMapName      string
	ConfigMapNamespace string
	ResyncPeriod       time.Duration
}

// Controller watches Deployments and StatefulSets and ensures VPAs exist.
type Controller struct {
	dynamicClient        dynamic.Interface
	config               Config
	policy               *exclusion.ConfigMapWatcher
	deploymentInformer   cache.SharedIndexInformer
	statefulSetInformer  cache.SharedIndexInformer
	logger               *slog.Logger
}

func NewController(clientset kubernetes.Interface, dynamicClient dynamic.Interface, cfg Config) *Controller {
	if cfg.ResyncPeriod == 0 {
		cfg.ResyncPeriod = defaultResyncPeriod
	}
	if len(cfg.BaseExclusion.Namespaces) == 0 && len(cfg.BaseExclusion.Workloads) == 0 {
		cfg.BaseExclusion = exclusion.ParseNamespaces("kube-system")
	}

	policyWatcher := exclusion.NewConfigMapWatcher(
		clientset,
		cfg.ConfigMapNamespace,
		cfg.ConfigMapName,
		cfg.BaseExclusion,
	)

	return &Controller{
		dynamicClient: dynamicClient,
		config:        cfg,
		policy:        policyWatcher,
		logger:        slog.Default().With("component", "vpa-provisioner"),
	}
}

func (c *Controller) Run(ctx context.Context, clientset kubernetes.Interface) error {
	if err := c.policy.LoadInitial(ctx); err != nil {
		return fmt.Errorf("load exclusion policy: %w", err)
	}

	go func() {
		if err := c.policy.Watch(ctx); err != nil && ctx.Err() == nil {
			c.logger.Error("exclusion configmap watcher stopped", "error", err)
		}
	}()

	factory := informers.NewSharedInformerFactory(clientset, c.config.ResyncPeriod)

	deploymentInformer := factory.Apps().V1().Deployments().Informer()
	statefulSetInformer := factory.Apps().V1().StatefulSets().Informer()
	c.deploymentInformer = deploymentInformer
	c.statefulSetInformer = statefulSetInformer

	c.policy.OnPolicyChange(func() {
		c.reconcileAll(ctx)
	})

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handleObject(ctx, obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			c.handleObject(ctx, newObj)
		},
	}

	if _, err := deploymentInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("register deployment handler: %w", err)
	}
	if _, err := statefulSetInformer.AddEventHandler(handler); err != nil {
		return fmt.Errorf("register statefulset handler: %w", err)
	}

	factory.Start(ctx.Done())

	c.logger.Info("waiting for informer caches to sync")
	if !cache.WaitForCacheSync(ctx.Done(), deploymentInformer.HasSynced, statefulSetInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer caches")
	}

	c.logger.Info("controller started")
	<-ctx.Done()
	c.logger.Info("controller stopped")
	return nil
}

func (c *Controller) reconcileAll(ctx context.Context) {
	c.logger.Info("exclusion policy changed; reconciling cached workloads")
	for _, obj := range c.deploymentInformer.GetStore().List() {
		c.handleObject(ctx, obj)
	}
	for _, obj := range c.statefulSetInformer.GetStore().List() {
		c.handleObject(ctx, obj)
	}
}

func (c *Controller) handleObject(ctx context.Context, obj interface{}) {
	ref, ok := workloadRefFromObject(obj)
	if !ok {
		return
	}

	policy := c.policy.Current()
	if policy.ShouldSkip(ref.exclusionRef()) {
		c.logger.Debug("workload skipped",
			"kind", ref.Kind,
			"namespace", ref.Namespace,
			"name", ref.Name,
		)
		return
	}

	if err := ensureVPAExists(ctx, c.dynamicClient, ref, policy); err != nil {
		c.logger.Error("failed to ensure VPA",
			"kind", ref.Kind,
			"namespace", ref.Namespace,
			"name", ref.Name,
			"error", err,
		)
		return
	}

	c.logger.Debug("VPA ensured",
		"kind", ref.Kind,
		"namespace", ref.Namespace,
		"name", ref.Name,
		"vpa", vpaNameFor(ref.Kind, ref.Name),
	)
}
