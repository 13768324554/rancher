package networkpolicy

import (
	"sort"

	"github.com/rancher/types/apis/core/v1"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	knetworkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// PodNameFieldLabel is used to specify the podName for pods
	// with hostPort specified
	PodNameFieldLabel = "field.cattle.io/podName"
)

type podHandler struct {
	npmgr            *netpolMgr
	pods             v1.PodInterface
	clusterNamespace string
}

func (ph *podHandler) Sync(key string, pod *corev1.Pod) error {
	if pod == nil || pod.DeletionTimestamp != nil {
		return nil
	}
	logrus.Debugf("podHandler: Sync: %+v", *pod)

	if err := ph.addLabelIfHostPortsPresent(pod); err != nil {
		return err
	}
	return ph.npmgr.hostPortsUpdateHandler(pod, ph.clusterNamespace)
}

// k8s native network policy can select pods only using labels,
// hence need to add a label which can be used to select this pod
// which has hostPorts
func (ph *podHandler) addLabelIfHostPortsPresent(pod *corev1.Pod) error {
	if pod.Labels != nil {
		if _, ok := pod.Labels[PodNameFieldLabel]; ok {
			return nil
		}
	}
	hasHostPorts := false
Loop:
	for _, c := range pod.Spec.Containers {
		for _, port := range c.Ports {
			if port.HostPort != 0 {
				hasHostPorts = true
				break Loop
			}
		}
	}
	if hasHostPorts {
		logrus.Debugf("podHandler: addLabelIfHostPortsPresent: pod=%+v has HostPort", *pod)
		podCopy := pod.DeepCopy()
		if podCopy.Labels == nil {
			podCopy.Labels = map[string]string{}
		}
		podCopy.Labels[PodNameFieldLabel] = podCopy.Name
		_, err := ph.pods.Update(podCopy)
		if err != nil {
			return err
		}
	}
	return nil
}

func (ph *podHandler) hostPortsUpdateHandler(pod *corev1.Pod) error {
	np := generatePodNetworkPolicy(pod)
	hasHostPorts := false
	for _, c := range pod.Spec.Containers {
		for _, port := range c.Ports {
			if port.HostPort != 0 {
				hp := intstr.FromInt(int(port.ContainerPort))
				proto := corev1.Protocol(port.Protocol)
				p := knetworkingv1.NetworkPolicyPort{
					Protocol: &proto,
					Port:     &hp,
				}
				np.Spec.Ingress[0].Ports = append(np.Spec.Ingress[0].Ports, p)
				hasHostPorts = true
			}
		}
	}
	if !hasHostPorts {
		return nil
	}

	// sort ports so it always appears in a certain order
	sort.Slice(np.Spec.Ingress[0].Ports, func(i, j int) bool {
		return portToString(np.Spec.Ingress[0].Ports[i]) < portToString(np.Spec.Ingress[0].Ports[j])
	})

	logrus.Debugf("netpolMgr: hostPortsUpdateHandler: pod=%+v has host ports, hence programming np=%+v", *pod, *np)
	return ph.npmgr.program(np)
}

func generatePodNetworkPolicy(pod *corev1.Pod) *knetworkingv1.NetworkPolicy {
	np := &knetworkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hp-" + pod.Name,
			Namespace: pod.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Pod",
					UID:        pod.UID,
					Name:       pod.Name,
				},
			},
		},
		Spec: knetworkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{PodNameFieldLabel: pod.Name},
			},
			Ingress: []knetworkingv1.NetworkPolicyIngressRule{
				{
					From:  []knetworkingv1.NetworkPolicyPeer{},
					Ports: []knetworkingv1.NetworkPolicyPort{},
				},
			},
		},
	}
	return np
}
