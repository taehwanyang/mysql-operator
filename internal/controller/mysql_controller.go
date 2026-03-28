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
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete

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

	// 1. Secret 확인
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      mysql.Spec.PasswordSecretName,
		Namespace: mysql.Namespace,
	}, &secret); err != nil {
		mysql.Status.Ready = false
		mysql.Status.Phase = "Error"
		mysql.Status.Message = "password secret not found"
		_ = r.Status().Update(ctx, &mysql)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 2. PVC 보장
	if err := r.reconcilePVC(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 3. Service 보장
	if err := r.reconcileService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 4. Deployment 보장
	if err := r.reconcileDeployment(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	// 5. 상태 반영
	mysql.Status.Ready = true
	mysql.Status.Phase = "Running"
	mysql.Status.Service = mysql.Name
	mysql.Status.Message = "resources reconciled"
	if err := r.Status().Update(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *MySQLReconciler) reconcileService(ctx context.Context, mysql *dbv1alpha1.MySQL) error {
	var svc corev1.Service
	err := r.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, &svc)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name,
			Namespace: mysql.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":   "mysql",
				"mysql": mysql.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name: "mysql",
					Port: mysql.Spec.Port,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(mysql, service, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, service)
}

func (r *MySQLReconciler) reconcilePVC(ctx context.Context, mysql *dbv1alpha1.MySQL) error {
	var pvc corev1.PersistentVolumeClaim
	err := r.Get(ctx, types.NamespacedName{Name: mysql.Name + "-data", Namespace: mysql.Namespace}, &pvc)
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

	pvc = corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name + "-data",
			Namespace: mysql.Namespace,
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
	}

	if err := ctrl.SetControllerReference(mysql, &pvc, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, &pvc)
}

func (r *MySQLReconciler) reconcileDeployment(ctx context.Context, mysql *dbv1alpha1.MySQL) error {
	var deploy appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: mysql.Name, Namespace: mysql.Namespace}, &deploy)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	labels := map[string]string{
		"app":   "mysql",
		"mysql": mysql.Name,
	}

	replicas := int32(1)

	deploy = appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name,
			Namespace: mysql.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
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
								{ContainerPort: mysql.Spec.Port},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "MYSQL_DATABASE",
									Value: mysql.Spec.Database,
								},
								{
									Name:  "MYSQL_USER",
									Value: mysql.Spec.User,
								},
								{
									Name: "MYSQL_PASSWORD",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: mysql.Spec.PasswordSecretName,
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
												Name: mysql.Spec.PasswordSecretName,
											},
											Key: "rootPassword",
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
					Volumes: []corev1.Volume{
						{
							Name: "data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: mysql.Name + "-data",
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(mysql, &deploy, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, &deploy)
}

// SetupWithManager sets up the controller with the Manager.
func (r *MySQLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1alpha1.MySQL{}).
		Named("mysql").
		Complete(r)
}
