package test

import (
	"context"
	"time"

	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/ginkgo/v2"
	//nolint:revive,stylecheck // dot import necessary for Ginkgo DSL
	. "github.com/onsi/gomega"

	"github.com/fluxcd/pkg/runtime/conditions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/open-component-model/ocm-k8s-toolkit/api/v1alpha1"
)

func SetupOCMRepositoryWithSpecData(ctx context.Context, k8sClient client.Client, namespace, repositoryName string, specData []byte) *v1alpha1.OCMRepository {
	GinkgoHelper()

	repository := &v1alpha1.OCMRepository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      repositoryName,
		},
		Spec: v1alpha1.OCMRepositorySpec{
			RepositorySpec: &apiextensionsv1.JSON{
				Raw: specData,
			},
			Interval: metav1.Duration{Duration: time.Minute * 10},
		},
	}
	Expect(k8sClient.Create(ctx, repository)).To(Succeed())

	conditions.MarkTrue(repository, "Ready", "ready", "message")
	Expect(k8sClient.Status().Update(ctx, repository)).To(Succeed())

	return repository
}
