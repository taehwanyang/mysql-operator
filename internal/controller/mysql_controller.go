/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1alpha1 "github.com/taehwanyang/mysql-operator/api/v1alpha1"
)

// MySQLReconciler reconciles a MySQL object
type MySQLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=db.ythwork.com,resources=mysqls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=db.ythwork.com,resources=mysqls/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MySQL object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile

func (r *MySQLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var mysql dbv1alpha1.MySQL
	if err := r.Get(ctx, req.NamespacedName, &mysql); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 1. root secret 확인
	_, err := r.getSecret(ctx, mysql.Namespace, mysql.Spec.RootPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "root password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 2. app secret 확인
	_, err = r.getSecret(ctx, mysql.Namespace, mysql.Spec.AppPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "app password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 3. repl secret 확인
	_, err = r.getSecret(ctx, mysql.Namespace, mysql.Spec.ReplPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "replication password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 4. headless service 보장
	if err := r.reconcileHeadlessService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 5. primary service 보장
	if err := r.reconcilePrimaryService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 6. replica service 보장
	if err := r.reconcileReplicaService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 7. statefulset 보장
	if err := r.reconcileStatefulSet(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 8. pod role label 보장
	if err := r.reconcilePodRoles(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &mysql, "Pending", "all required secrets and services found", 0); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MySQLReconciler) reconcileHeadlessService(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name,
			Namespace: mysql.Namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Selector: map[string]string{
				"app": mysql.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name: "mysql",
					Port: mysql.Spec.Port,
				},
			},
		},
	}
	return r.applyOwnedService(ctx, mysql, svc)
}

func (r *MySQLReconciler) reconcilePrimaryService(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name + "-primary",
			Namespace: mysql.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":  mysql.Name,
				"role": "primary",
			},
			Ports: []corev1.ServicePort{
				{
					Name: "mysql",
					Port: mysql.Spec.Port,
				},
			},
		},
	}
	return r.applyOwnedService(ctx, mysql, svc)
}

func (r *MySQLReconciler) reconcileReplicaService(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name + "-replicas",
			Namespace: mysql.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":  mysql.Name,
				"role": "replica",
			},
			Ports: []corev1.ServicePort{
				{
					Name: "mysql",
					Port: mysql.Spec.Port,
				},
			},
		},
	}
	return r.applyOwnedService(ctx, mysql, svc)
}

func (r *MySQLReconciler) reconcileStatefulSet(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	var existing appsv1.StatefulSet
	err := r.Get(ctx, types.NamespacedName{
		Name:      mysql.Name,
		Namespace: mysql.Namespace,
	}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	quantity, err := resource.ParseQuantity(mysql.Spec.StorageSize)
	if err != nil {
		return err
	}

	replicas := mysql.Spec.Replicas
	if replicas == 0 {
		replicas = 3
	}

	labels := map[string]string{
		"app": mysql.Name,
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name,
			Namespace: mysql.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: mysql.Name,
			Replicas:    &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mysql",
							Image: mysql.Spec.Image,
							Ports: []corev1.ContainerPort{
								{
									Name:          "mysql",
									ContainerPort: mysql.Spec.Port,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "MYSQL_DATABASE",
									Value: mysql.Spec.Database,
								},
								{
									Name:  "MYSQL_USER",
									Value: mysql.Spec.AppUser,
								},
								{
									Name: "MYSQL_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: mysql.Spec.AppPasswordSecretName,
											},
											Key: "password",
										},
									},
								},
								{
									Name: "MYSQL_ROOT_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: mysql.Spec.RootPasswordSecretName,
											},
											Key: "password",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "data",
									MountPath: "/var/lib/mysql",
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: quantity,
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(mysql, sts, r.Scheme); err != nil {
		return err
	}

	return r.Create(ctx, sts)
}

func (r *MySQLReconciler) reconcilePodRoles(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	var podList corev1.PodList
	if err := r.List(ctx, &podList,
		client.InNamespace(mysql.Namespace),
		client.MatchingLabels{"app": mysql.Name},
	); err != nil {
		return err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]

		expectedRole := "replica"
		if pod.Name == fmt.Sprintf("%s-0", mysql.Name) {
			expectedRole = "primary"
		}

		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}

		if pod.Labels["role"] == expectedRole {
			continue
		}

		updated := pod.DeepCopy()
		updated.Labels["role"] = expectedRole

		if err := r.Update(ctx, updated); err != nil {
			return err
		}
	}

	return nil
}

func (r *MySQLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1alpha1.MySQL{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *MySQLReconciler) getSecret(
	ctx context.Context,
	namespace string,
	name string,
) (*corev1.Secret, error) {
	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &secret)
	if err != nil {
		return nil, err
	}
	return &secret, nil
}

func (r *MySQLReconciler) updateStatus(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
	phase string,
	message string,
	readyReplicas int32,
) error {
	var latest dbv1alpha1.MySQL
	if err := r.Get(ctx, types.NamespacedName{
		Name:      mysql.Name,
		Namespace: mysql.Namespace,
	}, &latest); err != nil {
		return err
	}

	if latest.Status.Phase == phase &&
		latest.Status.Message == message &&
		latest.Status.ReadyReplicas == readyReplicas {
		return nil
	}

	latest.Status.Phase = phase
	latest.Status.Message = message
	latest.Status.ReadyReplicas = readyReplicas

	return r.Status().Update(ctx, &latest)
}

func (r *MySQLReconciler) applyOwnedService(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
	desired *corev1.Service,
) error {
	var existing corev1.Service
	err := r.Get(ctx, types.NamespacedName{
		Name:      desired.Name,
		Namespace: desired.Namespace,
	}, &existing)

	if err == nil {
		// 이미 있으면 지금 단계에서는 그냥 둠
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	if err := ctrl.SetControllerReference(mysql, desired, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, desired)
}
