package instancesnapshot_controller_test

import (
	"context"
	"fmt"
	"time"

	batch "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	crownlabsv1alpha2 "github.com/netgroup-polito/CrownLabs/operators/api/v1alpha2"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("InstancesnapshotController", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		InstanceName         = "test-instance"
		WorkingNamespace     = "working-namespace"
		TemplateName         = "test-template"
		TenantName           = "test-tenant"
		VmiNotRunningStatus  = "Vmioff"
		InstanceSnapshotName = "isnap-sample"

		timeout  = time.Second * 20
		interval = time.Millisecond * 500
	)

	var (
		workingNs = &v1.Namespace{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: WorkingNamespace,
				Labels: map[string]string{
					"test-suite": "true",
				},
			},
			Spec:   v1.NamespaceSpec{},
			Status: v1.NamespaceStatus{},
		}
		templateEnvironment = crownlabsv1alpha2.TemplateSpec{
			WorkspaceRef: crownlabsv1alpha2.GenericRef{},
			PrettyName:   "My Template",
			Description:  "Description of my template",
			EnvironmentList: []crownlabsv1alpha2.Environment{
				{
					Name:       "Env-1",
					GuiEnabled: true,
					Resources: crownlabsv1alpha2.EnvironmentResources{
						CPU:                   1,
						ReservedCPUPercentage: 1,
						Memory:                resource.MustParse("1024M"),
					},
					EnvironmentType: crownlabsv1alpha2.ClassVM,
					Persistent:      true,
					Image:           "crownlabs/vm",
				},
			},
			DeleteAfter: "",
		}
		template = &crownlabsv1alpha2.Template{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      TemplateName,
				Namespace: WorkingNamespace,
			},
			Spec:   templateEnvironment,
			Status: crownlabsv1alpha2.TemplateStatus{},
		}
		instance = &crownlabsv1alpha2.Instance{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      InstanceName,
				Namespace: WorkingNamespace,
			},
			Spec: crownlabsv1alpha2.InstanceSpec{
				Running: true,
				Template: crownlabsv1alpha2.GenericRef{
					Name:      TemplateName,
					Namespace: WorkingNamespace,
				},
				Tenant: crownlabsv1alpha2.GenericRef{
					Name: TenantName,
				},
			},
			Status: crownlabsv1alpha2.InstanceStatus{},
		}

		instanceSnapshot = &crownlabsv1alpha2.InstanceSnapshot{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name:      InstanceSnapshotName,
				Namespace: WorkingNamespace,
			},
			Spec: crownlabsv1alpha2.InstanceSnapshotSpec{
				Instance: crownlabsv1alpha2.GenericRef{
					Name:      InstanceName,
					Namespace: WorkingNamespace,
				},
				ImageName: "test-image",
			},
		}
	)

	ctx := context.Background()

	Context("Creating a snapshot of a persistent VM", func() {
		BeforeEach(func() {
			By("Creating the namespace where to create instance and template")
			Expect(k8sClient.Create(ctx, workingNs)).Should(Succeed())

			By("By checking that the namespace has been created")
			nsLookupKey := types.NamespacedName{Name: WorkingNamespace}
			createdNs := &v1.Namespace{}

			doesEventuallyExists(ctx, nsLookupKey, createdNs, BeTrue(), timeout, interval)

			By("Creating the template")
			Expect(k8sClient.Create(ctx, template)).Should(Succeed())

			By("By checking that the template has been created")
			templateLookupKey := types.NamespacedName{Name: TemplateName, Namespace: WorkingNamespace}
			createdTemplate := &crownlabsv1alpha2.Template{}

			doesEventuallyExists(ctx, templateLookupKey, createdTemplate, BeTrue(), timeout, interval)

			By("Creating the instance")
			Expect(k8sClient.Create(ctx, instance)).Should(Succeed())

			By("Checking that the instance has been created")
			instanceLookupKey := types.NamespacedName{Name: InstanceName, Namespace: WorkingNamespace}
			createdInstance := &crownlabsv1alpha2.Instance{}

			doesEventuallyExists(ctx, instanceLookupKey, createdInstance, BeTrue(), timeout, interval)
		})

		It("Should create a job in charge of snapshotting the instance when creating the InstanceSnapshot resource", func() {
			By("Updating specs and status of instance in order to set it as not running")
			instance.Status.Phase = VmiNotRunningStatus
			Expect(k8sClient.Update(ctx, instance)).Should(Succeed())

			instanceLookupKey := types.NamespacedName{Name: InstanceName, Namespace: WorkingNamespace}
			createdInstance := &crownlabsv1alpha2.Instance{}

			doesEventuallyExists(ctx, instanceLookupKey, createdInstance, BeTrue(), timeout, interval)

			fmt.Println("Phase is" + createdInstance.Status.Phase)

			By("Creating the InstanceSnapshot resource")
			Expect(k8sClient.Create(ctx, instanceSnapshot)).Should(Succeed())

			By("Checking that the instance has been created")
			instanceSnapshotLookupKey := types.NamespacedName{Name: InstanceSnapshotName, Namespace: WorkingNamespace}
			createdInstanceSnapshot := &crownlabsv1alpha2.InstanceSnapshot{}

			doesEventuallyExists(ctx, instanceSnapshotLookupKey, createdInstanceSnapshot, BeTrue(), timeout, interval)

			By("Checking if the job for the creation of the snapshot has been created")
			jobLookupKey := types.NamespacedName{Name: InstanceSnapshotName, Namespace: WorkingNamespace}
			createdJob := &batch.Job{}

			doesEventuallyExists(ctx, jobLookupKey, createdJob, BeTrue(), timeout, interval)
		})
	})
})
