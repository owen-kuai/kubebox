package operator

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KubePodClient struct {
	Client client.Client
}

func (k *KubePodClient) Get(namespace, name string) (Pod, error) {
	var object corev1.Pod
	if err := k.Client.Get(context.Background(), client.ObjectKey{Namespace: namespace, Name: name}, &object); err != nil {
		if apierrors.IsNotFound(err) {
			return Pod{}, ErrNotFound
		}
		return Pod{}, err
	}
	return podFromKube(object), nil
}

func (k *KubePodClient) Create(pod Pod) error {
	object := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: pod.Namespace, Name: pod.Name, Labels: pod.Labels},
		Spec: corev1.PodSpec{
			RuntimeClassName:             stringPtr(pod.RuntimeClass),
			Containers:                   []corev1.Container{{Name: "envd", Image: "kubebox-envd:dev", Ports: []corev1.ContainerPort{{Name: "grpc", ContainerPort: 50051}}}},
			AutomountServiceAccountToken: boolPtr(false),
			SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: boolPtr(true)},
		},
	}
	if err := k.Client.Create(context.Background(), object); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (k *KubePodClient) Delete(namespace, name string) error {
	object := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if err := k.Client.Delete(context.Background(), object); err != nil {
		if apierrors.IsNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func podFromKube(object corev1.Pod) Pod {
	ready, healthy := false, false
	for _, condition := range object.Status.Conditions {
		if condition.Type == corev1.PodReady {
			ready = condition.Status == corev1.ConditionTrue
		}
		if condition.Type == corev1.PodInitialized {
			healthy = condition.Status == corev1.ConditionTrue
		}
	}
	if len(object.Status.ContainerStatuses) > 0 {
		healthy = healthy && object.Status.ContainerStatuses[0].Ready
	}
	return Pod{Namespace: object.Namespace, Name: object.Name, RuntimeClass: valueOrEmpty(object.Spec.RuntimeClassName), Labels: object.Labels, Ready: ready, Healthy: healthy, Deleted: object.DeletionTimestamp != nil}
}

func stringPtr(v string) *string { return &v }
func boolPtr(v bool) *bool       { return &v }
func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

var _ PodClient = (*KubePodClient)(nil)
var _ = errors.Is
