AI-generated content prepared on Will's behalf.

# Alertmanager Route Tester Deployment Design

## Goal

Make Alertmanager Route Tester deployable as a container through a reusable Helm chart in this repository.

## Image

Add a multi-stage Docker build that compiles the Go application and runs it as a non-root user in a minimal runtime image with system CA roots. Extend the existing release workflow to publish version-tagged images to `ghcr.io/linode-obs/alertmanager-route-tester` on `v*` tags, with `latest` updated for stable tags. Do not change GHCR package visibility. This work configures publishing but does not push an image.

## Helm chart

Add `helm/alertmanager-route-tester` with a ConfigMap generated from named Alertmanager settings, a Deployment, and a ClusterIP Service. The Deployment mounts the config at `/etc/alertmanager-route-tester/config.yaml`, passes that path with `-config`, and uses a checksum annotation so config changes roll out new pods. The chart binds the app to `0.0.0.0:8080`; `service.port` changes the Service port while the named container port remains the target. It supports image-pull Secrets and read-only external Secret mounts for TLS files. TCP probes check the app listener without coupling pod health to Alertmanager availability. It disables service-account token mounting, sets non-root pod and container security contexts, and declares resource requests without limits. A default-deny ingress NetworkPolicy allows configured trusted sources. The chart does not create an Ingress or enable a cluster deployment. Alertmanager URLs must not contain credentials because the app logs the configured URL.

The chart exposes `extraObjects` as a list of templated Kubernetes object maps for resources not provided directly by the chart. Helm replaces lists when later values files override them, so values overlays must supply the complete list. The chart also exposes `deploymentAnnotations` on Deployment metadata for external controllers. Document the Stakater Reloader Secret annotation as an option when a Reloader controller is already installed cluster-wide; do not install a controller from this application chart.

## Scope

All changes stay in the Alertmanager Route Tester repository. Do not modify `~/repos/forks/o11y-helm-charts`, add Argo CD wiring, or enable deployment in a cluster. The chart path can be referenced from that repository later, following the existing `latr` convention.

## Verification

Run Go tests and build, build the Docker image locally without publishing, and run the chart test script. It validates `helm lint`, app config rendering, Secret mounts, NetworkPolicy behavior, checksum rollouts, an empty `extraObjects` list, a templated extra object, and Deployment annotations. Use the Route Tester application config schema, not Alertmanager's server config.
