package namespace

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/peak-scale/observability-tenancy/internal/config"
	"github.com/peak-scale/observability-tenancy/internal/metrics"
	"github.com/peak-scale/observability-tenancy/internal/stores"
)

type StoreController struct {
	client.Client
	Metrics *metrics.ProxyRecorder
	Scheme  *runtime.Scheme
	Store   *stores.NamespaceStore
	Log     logr.Logger
	Config  *config.Config
}

func (r *StoreController) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).For(&corev1.Namespace{})

	// If a selector is provided, add an event filter so that only matching tenants trigger reconcile.
	if r.Config.Selector.Selector() != nil {
		selector, err := metav1.LabelSelectorAsSelector(r.Config.Selector.Selector())
		if err != nil {
			return fmt.Errorf("invalid label selector: %w", err)
		}

		builder = builder.WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return selector.Matches(labels.Set(e.Object.GetLabels()))
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				return selector.Matches(labels.Set(e.ObjectNew.GetLabels()))
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return selector.Matches(labels.Set(e.Object.GetLabels()))
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return selector.Matches(labels.Set(e.Object.GetLabels()))
			},
		})
	}

	return builder.Complete(r)
}

func (r *StoreController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	origin := &corev1.Namespace{}
	if err := r.Get(ctx, req.NamespacedName, origin); err != nil {
		r.lifecycle(&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
			},
		})

		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	r.Store.Update(origin, r.Config.Tenant)

	return ctrl.Result{}, nil
}

// First execttion of the controller to load the settings (without manager cache).
func (r *StoreController) Init(ctx context.Context, c client.Client) (err error) {
	tnts := &corev1.NamespaceList{}

	var opts []client.ListOption

	// If a selector is provided, add it as a list option.
	if r.Config.Selector.Selector() != nil {
		selector, err := metav1.LabelSelectorAsSelector(r.Config.Selector.Selector())
		if err != nil {
			return fmt.Errorf("invalid label selector: %w", err)
		}

		opts = append(opts, client.MatchingLabelsSelector{Selector: selector})
	}

	if err := c.List(ctx, tnts, opts...); err != nil {
		return fmt.Errorf("could not load tenants: %w", err)
	}

	for _, tnt := range tnts.Items {
		r.Store.Update(&tnt, r.Config.Tenant)
	}

	return
}

func (r *StoreController) lifecycle(ns *corev1.Namespace) {
	r.Store.Delete(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ns.Name,
			Namespace: ns.Namespace,
		},
	})

	r.Metrics.DeleteMetricsForNamespace(ns)
}
