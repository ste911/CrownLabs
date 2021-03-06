package instance_creation

import (
	"encoding/base64"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
)

// ForgeService creates and returns a Kubernetes Service resource providing
// access to a CrownLabs environment.
func ForgeService(name, namespace string) corev1.Service {
	service := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "vnc",
					Protocol:   corev1.ProtocolTCP,
					Port:       6080,
					TargetPort: intstr.IntOrString{IntVal: 6080},
				},
				{
					Name:       "ssh",
					Protocol:   corev1.ProtocolTCP,
					Port:       22,
					TargetPort: intstr.IntOrString{IntVal: 22},
				},
			},
			Selector:  map[string]string{"name": name},
			ClusterIP: "",
			Type:      corev1.ServiceTypeClusterIP,
		},
	}

	return service
}

// ForgeIngress creates and returns a Kubernetes Ingress resource providing
// exposing the remote desktop of a CrownLabs environment.
func ForgeIngress(name, namespace string, svc *corev1.Service, urlUUID, websiteBaseURL string) networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	url := websiteBaseURL + "/" + urlUUID

	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    nil,
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target":        "/$2",
				"nginx.ingress.kubernetes.io/proxy-read-timeout":    "3600",
				"nginx.ingress.kubernetes.io/proxy-send-timeout":    "3600",
				"nginx.ingress.kubernetes.io/auth-signin":           "https://$host/" + urlUUID + "/oauth2/start?rd=$escaped_request_uri",
				"nginx.ingress.kubernetes.io/auth-url":              "https://$host/" + urlUUID + "/oauth2/auth",
				"crownlabs.polito.it/probe-url":                     "https://" + url,
				"nginx.ingress.kubernetes.io/configuration-snippet": `sub_filter '<head>' '<head> <base href="https://$host/` + urlUUID + `/index.html">';`,
			},
		},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{websiteBaseURL},
					SecretName: "crownlabs-ingress-secret",
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: websiteBaseURL,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/" + urlUUID + "(/|$)(.*)",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: svc.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: svc.Spec.Ports[0].TargetPort.IntVal,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return ingress
}

// ForgeOauth2Deployment creates and returns a Kubernetes Deployment resource
// for oauth2-proxy, which is used to enforce authentication when connecting
// to the remote desktop of a CrownLabs environment.
func ForgeOauth2Deployment(name, namespace, urlUUID, image, clientSecret, providerURL string) appsv1.Deployment {
	cookieUUID := uuid.New().String()
	id, _ := uuid.New().MarshalBinary()
	cookieSecret := base64.StdEncoding.EncodeToString(id)
	labels := generateOauth2Labels(name)

	deploy := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-oauth2",
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32Ptr(1),
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
							Name:  name,
							Image: image,
							Args: []string{
								"--http-address=0.0.0.0:4180",
								"--reverse-proxy=true",
								"--skip-provider-button=true",
								"--cookie-secret=" + cookieSecret,
								"--cookie-expire=24h",
								"--cookie-name=_oauth2_cookie_" + string([]rune(cookieUUID)[:6]),
								"--provider=keycloak",
								"--client-id=k8s",
								"--client-secret=" + clientSecret,
								"--login-url=" + providerURL + "/protocol/openid-connect/auth",
								"--redeem-url=" + providerURL + "/protocol/openid-connect/token",
								"--validate-url=" + providerURL + "/protocol/openid-connect/userinfo",
								"--proxy-prefix=/" + urlUUID + "/oauth2",
								"--cookie-path=/" + urlUUID,
								"--email-domain=*",
								"--session-cookie-minimal=true",
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 4180,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("50m"),
									corev1.ResourceMemory: resource.MustParse("100Mi"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("10m"),
									corev1.ResourceMemory: resource.MustParse("25Mi"),
								},
							},
						},
					},
				},
			},
		},
	}

	return deploy
}

// ForgeOauth2Service creates and returns a Kubernetes Service resource
// for oauth2-proxy, which is used to enforce authentication when connecting
// to the remote desktop of a CrownLabs environment.
func ForgeOauth2Service(name, namespace string) corev1.Service {
	labels := generateOauth2Labels(name)
	service := corev1.Service{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-oauth2",
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       4180,
					TargetPort: intstr.IntOrString{IntVal: 4180},
				},
			},
			Selector: labels,
		},
	}

	return service
}

// ForgeOauth2Ingress creates and returns a Kubernetes Ingress resource
// for oauth2-proxy, which is used to enforce authentication when connecting
// to the remote desktop of a CrownLabs environment.
func ForgeOauth2Ingress(name, namespace string, svc *corev1.Service, urlUUID, websiteBaseURL string) networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	ingress := networkingv1.Ingress{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-oauth2",
			Namespace: namespace,
			Labels:    generateOauth2Labels(name),
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/cors-allow-credentials": "true",
				"nginx.ingress.kubernetes.io/cors-allow-headers":     "DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type,Authorization",
				"nginx.ingress.kubernetes.io/cors-allow-methods":     "PUT, GET, POST, OPTIONS, DELETE, PATCH",
				"nginx.ingress.kubernetes.io/cors-allow-origin":      "https://*",
				"nginx.ingress.kubernetes.io/enable-cors":            "true",
			},
		},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{
				{
					Hosts:      []string{websiteBaseURL},
					SecretName: "crownlabs-ingress-secret",
				},
			},
			Rules: []networkingv1.IngressRule{
				{
					Host: websiteBaseURL,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/" + urlUUID + "/oauth2/.*",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: svc.Name,
											Port: networkingv1.ServiceBackendPort{
												Number: svc.Spec.Ports[0].TargetPort.IntVal,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return ingress
}

// generateOauth2Labels returns a map of labels common to all oauth2-proxy resources.
func generateOauth2Labels(instanceName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/part-of":   instanceName,
		"app.kubernetes.io/component": "oauth2-proxy",
	}
}
