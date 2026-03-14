package httpd

import (
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

const testNS = "default"

func createMachine(name, mac string) *isobootgithubiov1alpha1.Machine {
	m := &isobootgithubiov1alpha1.Machine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       isobootgithubiov1alpha1.MachineSpec{MAC: mac},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, m)).To(Succeed())
	return m
}

func createProvision(
	name, machineRef, bootConfigRef string,
	phase isobootgithubiov1alpha1.ProvisionPhase,
) *isobootgithubiov1alpha1.Provision {
	p := &isobootgithubiov1alpha1.Provision{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: isobootgithubiov1alpha1.ProvisionSpec{
			MachineRef:         machineRef,
			BootConfigRef:      bootConfigRef,
			ProvisionAnswerRef: "answer-1",
		},
	}
	ExpectWithOffset(1, k8sClient.Create(ctx, p)).To(Succeed())
	if phase != "" {
		p.Status.Phase = phase
		ExpectWithOffset(1, k8sClient.Status().Update(ctx, p)).To(Succeed())
	}
	return p
}
