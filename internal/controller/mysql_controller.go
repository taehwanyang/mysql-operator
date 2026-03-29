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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dbv1alpha1 "github.com/taehwanyang/mysql-operator/api/v1alpha1"
)

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
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *MySQLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var mysql dbv1alpha1.MySQL
	if err := r.Get(ctx, req.NamespacedName, &mysql); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	_, err := r.getSecret(ctx, mysql.Namespace, mysql.Spec.RootPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "root password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	_, err = r.getSecret(ctx, mysql.Namespace, mysql.Spec.AppPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "app password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	_, err = r.getSecret(ctx, mysql.Namespace, mysql.Spec.ReplPasswordSecretName)
	if err != nil {
		_ = r.updateStatus(ctx, &mysql, "Error", "replication password secret not found", 0)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if err := r.reconcileHeadlessService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcilePrimaryService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileReplicaService(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcilePrimaryBootstrapConfigMap(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileReplicationConfigMap(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileStatefulSet(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcilePodRoles(ctx, &mysql); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &mysql, "Running", "all required secrets and services found", 0); err != nil {
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
					InitContainers: []corev1.Container{
						r.mysqlConfigInitContainer(),
					},
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
								{
									Name:      "mysql-config",
									MountPath: "/etc/mysql/conf.d",
								},
								{
									Name:      "root-secret",
									MountPath: "/etc/mysql-secrets/root",
									ReadOnly:  true,
								},
								{
									Name:      "app-secret",
									MountPath: "/etc/mysql-secrets/app",
									ReadOnly:  true,
								},
								{
									Name:      "repl-secret",
									MountPath: "/etc/mysql-secrets/repl",
									ReadOnly:  true,
								},
							},
						},
						{
							Name:  "primary-bootstrap",
							Image: mysql.Spec.Image,
							Command: []string{
								"sh",
								"-c",
								`/opt/bootstrap/bootstrap-primary.sh && tail -f /dev/null`,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "primary-bootstrap",
									MountPath: "/opt/bootstrap",
								},
								{
									Name:      "root-secret",
									MountPath: "/etc/mysql-secrets/root",
									ReadOnly:  true,
								},
								{
									Name:      "app-secret",
									MountPath: "/etc/mysql-secrets/app",
									ReadOnly:  true,
								},
								{
									Name:      "repl-secret",
									MountPath: "/etc/mysql-secrets/repl",
									ReadOnly:  true,
								},
							},
						},
						{
							Name:  "replication-bootstrap",
							Image: mysql.Spec.Image,
							Command: []string{
								"sh",
								"-c",
								`/opt/bootstrap/bootstrap-replication.sh && tail -f /dev/null`,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "replication-bootstrap",
									MountPath: "/opt/bootstrap",
								},
								{
									Name:      "root-secret",
									MountPath: "/etc/mysql-secrets/root",
									ReadOnly:  true,
								},
								{
									Name:      "repl-secret",
									MountPath: "/etc/mysql-secrets/repl",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "mysql-config",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "primary-bootstrap",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: mysql.Name + "-primary-bootstrap",
									},
									DefaultMode: func() *int32 { m := int32(0755); return &m }(),
								},
							},
						},
						{
							Name: "replication-bootstrap",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: mysql.Name + "-replication-bootstrap",
									},
									DefaultMode: func() *int32 { m := int32(0755); return &m }(),
								},
							},
						},
						{
							Name: "root-secret",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: mysql.Spec.RootPasswordSecretName,
								},
							},
						},
						{
							Name: "repl-secret",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: mysql.Spec.ReplPasswordSecretName,
								},
							},
						},
						{
							Name: "app-secret",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: mysql.Spec.AppPasswordSecretName,
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
		name := podList.Items[i].Name

		expectedRole := "replica"
		if name == fmt.Sprintf("%s-0", mysql.Name) {
			expectedRole = "primary"
		}

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest corev1.Pod
			if err := r.Get(ctx, types.NamespacedName{
				Name:      name,
				Namespace: mysql.Namespace,
			}, &latest); err != nil {
				return client.IgnoreNotFound(err)
			}

			if latest.Labels == nil {
				latest.Labels = map[string]string{}
			}
			if latest.Labels["role"] == expectedRole {
				return nil
			}

			base := latest.DeepCopy()
			latest.Labels["role"] = expectedRole
			return r.Patch(ctx, &latest, client.MergeFrom(base))
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *MySQLReconciler) reconcileReplicationConfigMap(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name + "-replication-bootstrap",
			Namespace: mysql.Namespace,
		},
		Data: map[string]string{
			"bootstrap-replication.sh": fmt.Sprintf(`#!/bin/sh
set -eu

ordinal=${HOSTNAME##*-}

if [ "$ordinal" -eq 0 ]; then
  echo "primary pod, skip replica bootstrap"
  exit 0
fi

ROOT_PASSWORD="$(cat /etc/mysql-secrets/root/password)"
REPL_PASSWORD="$(cat /etc/mysql-secrets/repl/password)"

PRIMARY_HOST="%s-0.%s"
MYSQL_PORT="%d"

echo "waiting for local mysql..."
until mysqladmin ping -h 127.0.0.1 -uroot -p"${ROOT_PASSWORD}" --silent; do
  sleep 3
done

echo "waiting for primary mysql..."
until mysqladmin ping -h "${PRIMARY_HOST}" -P "${MYSQL_PORT}" -uroot -p"${ROOT_PASSWORD}" --silent; do
  sleep 3
done

echo "configure replica..."
mysql -h 127.0.0.1 -uroot -p"${ROOT_PASSWORD}" <<EOSQL
STOP REPLICA;
RESET REPLICA ALL;
CHANGE REPLICATION SOURCE TO
  SOURCE_HOST='%s-0.%s',
  SOURCE_PORT=%d,
  SOURCE_USER='repl',
  SOURCE_PASSWORD='${REPL_PASSWORD}',
  SOURCE_AUTO_POSITION=1,
  GET_SOURCE_PUBLIC_KEY=1;
START REPLICA;
SET GLOBAL read_only = ON;
SET GLOBAL super_read_only = ON;
EOSQL

echo "replica bootstrap complete"
`, mysql.Name, mysql.Name, mysql.Spec.Port, mysql.Name, mysql.Name, mysql.Spec.Port),
		},
	}

	return r.applyOwnedConfigMap(ctx, mysql, cm)
}

func (r *MySQLReconciler) reconcilePrimaryBootstrapConfigMap(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysql.Name + "-primary-bootstrap",
			Namespace: mysql.Namespace,
		},
		Data: map[string]string{
			"bootstrap-primary.sh": `#!/bin/sh
set -eu

ordinal=${HOSTNAME##*-}
if [ "$ordinal" -ne 0 ]; then
  echo "not primary pod, skip primary bootstrap"
  exit 0
fi

APP_PASSWORD="$(cat /etc/mysql-secrets/app/password)"
REPL_PASSWORD="$(cat /etc/mysql-secrets/repl/password)"
ROOT_PASSWORD="$(cat /etc/mysql-secrets/root/password)"

echo "waiting for local mysql..."
until mysqladmin ping -h 127.0.0.1 -uroot -p"${ROOT_PASSWORD}" --silent; do
  sleep 3
done

echo "configuring primary..."
mysql -h 127.0.0.1 -uroot -p"${ROOT_PASSWORD}" <<EOSQL
CREATE DATABASE IF NOT EXISTS appdb;

CREATE USER IF NOT EXISTS 'appuser'@'%' IDENTIFIED BY '${APP_PASSWORD}';
ALTER USER 'appuser'@'%' IDENTIFIED BY '${APP_PASSWORD}';
GRANT ALL PRIVILEGES ON appdb.* TO 'appuser'@'%';

CREATE USER IF NOT EXISTS 'repl'@'%' IDENTIFIED BY '${REPL_PASSWORD}';
ALTER USER 'repl'@'%' IDENTIFIED BY '${REPL_PASSWORD}';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'repl'@'%';

FLUSH PRIVILEGES;
EOSQL

echo "primary bootstrap complete"
`,
		},
	}

	return r.applyOwnedConfigMap(ctx, mysql, cm)
}

func (r *MySQLReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dbv1alpha1.MySQL{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.StatefulSet{}).
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

func (r *MySQLReconciler) mysqlConfigInitContainer() corev1.Container {
	return corev1.Container{
		Name:  "mysql-config-init",
		Image: "busybox:1.36",
		Command: []string{
			"sh",
			"-c",
			`
ordinal=${HOSTNAME##*-}

cat > /mnt/conf.d/server-id.cnf <<EOF
[mysqld]
server-id=$((100 + ordinal))
bind-address=0.0.0.0
log-bin=mysql-bin
binlog_format=ROW
gtid_mode=ON
enforce_gtid_consistency=ON
log_replica_updates=ON
EOF
`,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "mysql-config",
				MountPath: "/mnt/conf.d",
			},
		},
	}
}

func (r *MySQLReconciler) applyOwnedConfigMap(
	ctx context.Context,
	mysql *dbv1alpha1.MySQL,
	desired *corev1.ConfigMap,
) error {
	var existing corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{
		Name:      desired.Name,
		Namespace: desired.Namespace,
	}, &existing)

	if err == nil {
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
