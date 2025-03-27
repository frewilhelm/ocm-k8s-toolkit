package ocmdeployer

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/fluxcd/pkg/runtime/conditions"
	"github.com/fluxcd/pkg/runtime/patch"
	krov1alpha1 "github.com/kro-run/kro/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	yamlapi "k8s.io/apimachinery/pkg/util/yaml"
	"ocm.software/ocm/api/datacontext"
	ocmctx "ocm.software/ocm/api/ocm"
	"ocm.software/ocm/api/ocm/compdesc"
	v1 "ocm.software/ocm/api/ocm/compdesc/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	deliveryv1alpha1 "github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
	resource2 "github.com/open-component-model/ocm-k8s-toolkit/internal/controller/resource"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ociartifact"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/ocm"
	"github.com/open-component-model/ocm-k8s-toolkit/pkg/status"
)

// Reconciler reconciles a OCMDeployer object
type Reconciler struct {
	*ocm.BaseReconciler
	Registry *ociartifact.Registry
}

var _ ocm.Reconciler = (*Reconciler)(nil)

// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=delivery.ocm.software,resources=ocmdeployers/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deliveryv1alpha1.OCMDeployer{}).
		Named("ocmdeployer").
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the OCMDeployer object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	ocmDeployer := &deliveryv1alpha1.OCMDeployer{}
	if err := r.Get(ctx, req.NamespacedName, ocmDeployer); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper := patch.NewSerialPatcher(ocmDeployer, r.Client)

	if ocmDeployer.Spec.Suspend {
		return ctrl.Result{}, nil
	}

	if ocmDeployer.GetDeletionTimestamp() != nil {
		return ctrl.Result{Requeue: true}, nil
	}

	resource := &deliveryv1alpha1.Resource{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      ocmDeployer.Spec.ResourceRef.Name,
	}, resource); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get resource: %w", err)
	}

	if resource.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, errors.New("resource is being deleted")
	}

	if !conditions.IsReady(resource) {
		return ctrl.Result{}, errors.New("resource is not ready")
	}

	component := &deliveryv1alpha1.Component{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: "default",
		Name:      resource.Spec.ComponentRef.Name,
	}, component); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get component: %w", err)
	}

	if component.GetDeletionTimestamp() != nil {
		return ctrl.Result{}, errors.New("component is being deleted")
	}

	if !conditions.IsReady(component) {
		return ctrl.Result{}, errors.New("component is not ready")
	}

	logger := log.FromContext(ctx)
	logger.V(1).Info("reconciling resource")

	octx := ocmctx.New(datacontext.MODE_EXTENDED)
	session := ocmctx.NewSession(datacontext.NewSession())
	// automatically close the session when the ocm context is closed in the above defer
	octx.Finalizer().Close(session)

	// Create repository to download the component descriptors
	repositoryCD, err := r.Registry.NewRepository(ctx, component.GetOCIRepository())
	if err != nil {
		return ctrl.Result{}, err
	}

	// Get component descriptor set from artifact
	data, err := repositoryCD.FetchArtifact(ctx, component.GetManifestDigest())
	if err != nil {
		return ctrl.Result{}, err
	}

	cds := &ocm.Descriptors{}
	if err := yamlapi.NewYAMLToJSONDecoder(bytes.NewReader(data)).Decode(cds); err != nil {
		return ctrl.Result{}, err
	}

	cdSet := compdesc.NewComponentVersionSet(cds.List...)

	// Get referenced component descriptor from component descriptor set
	cd, err := cdSet.LookupComponentVersion(component.Status.Component.Component, component.Status.Component.Version)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to lookup component descriptor: %w", err)
	}

	// Get resource, respective component descriptor and component version
	resourceReference := v1.ResourceReference{
		Resource:      resource.Spec.Resource.ByReference.Resource,
		ReferencePath: resource.Spec.Resource.ByReference.ReferencePath,
	}

	// Resolve resource resourceReference to get resource and its component descriptor
	resourceDesc, resourceCompDesc, err := compdesc.ResolveResourceReference(cd, resourceReference, cdSet)
	if err != nil {

		return ctrl.Result{}, fmt.Errorf("failed to resolve resource reference: %w", err)
	}

	cv, err := resource2.GetComponentVersion(ctx, octx, session, component.Status.Component.RepositorySpec.Raw, resourceCompDesc)
	if err != nil {
		return ctrl.Result{}, err
	}

	resourceAccess, err := resource2.GetResourceAccess(ctx, cv, resourceDesc, resourceCompDesc)
	if err != nil {
		return ctrl.Result{}, err
	}

	blobAccess, err := resource2.GetBlobAccess(ctx, resourceAccess)
	if err != nil {
		return ctrl.Result{}, err
	}

	manifest, err := blobAccess.Get()
	if err != nil {
		return ctrl.Result{}, err
	}

	// 2. Apply RGD resource
	var rgd krov1alpha1.ResourceGraphDefinition
	// Unmarshal the manifest into the ResourceGraphDefinition object
	if err := yaml.Unmarshal(manifest, &rgd); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	// Create or update the object in the cluster
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, &rgd, func() error {
		if err := controllerutil.SetControllerReference(ocmDeployer, &rgd, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on resource graph definition: %w", err)
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create or update resource graph definition: %w", err)
	}

	// TODO: Drift detection required?

	status.MarkReady(r.EventRecorder, ocmDeployer, "Applied version")
	logger.Info("ocm deployer is ready", "name", ocmDeployer.GetName())

	if err := status.UpdateStatus(ctx, patchHelper, ocmDeployer, r.EventRecorder, ocmDeployer.GetRequeueAfter(), err); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: resource.GetRequeueAfter()}, nil
}
